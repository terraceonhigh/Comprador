// Package osfs is a read-only Galatea FSAL backed by the local operating
// system filesystem. It maps os.ReadDir / os.Open / os.Stat onto the
// virtual.Directory / virtual.Leaf interface, so a real directory tree
// can be navigated through Galatea's filesystem abstraction layer.
//
// It is the first Galatea backend over an externally-defined data source
// (the in-memory FSAL is a test fixture); see docs/DECISIONS.md DEC-006.
// Read-only by design: every mutating operation returns StatusErrROFS.
//
// File handles (DEC-017 Option B) are the node's path relative to the FSAL
// root; NewHandleResolver resolves them back. This bounds a handle's length
// by the path length — fine for ordinary trees, but a deeply-nested path can
// exceed NFS4_FHSIZE (128 bytes); an inode/hash scheme would lift that and is
// the natural future refinement. The mandatory attributes the NFSv4 server
// requires (FileHandle, HasNamedAttributes, IsInNamedAttributeDirectory — see
// MISTAKES.md M-006) are all set here.
package osfs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/terraceonhigh/galatea/pkg/virtual"
)

// Root returns a virtual.Directory rooted at the given host path. The
// path must name an existing directory.
func Root(hostPath string) (virtual.Directory, error) {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "root", Path: abs, Err: syscall.ENOTDIR}
	}
	return &directory{path: abs, root: abs}, nil
}

// osfsHandle encodes a node's file handle as its path relative to the FSAL
// root. The root itself encodes as ".".
func osfsHandle(root, hostPath string) []byte {
	rel, err := filepath.Rel(root, hostPath)
	if err != nil {
		rel = "."
	}
	return []byte(rel)
}

// NewHandleResolver returns a HandleResolver (DEC-017 Option B) for an osfs
// tree rooted at rootDir, which must be an *osfs directory. It decodes the
// path-relative handle, rejects any handle that escapes the root, stats the
// target, and returns its node.
func NewHandleResolver(rootDir virtual.Directory) virtual.HandleResolver {
	d, ok := rootDir.(*directory)
	if !ok {
		// Not an osfs root; resolve nothing.
		return func(io.ByteReader) (virtual.DirectoryChild, virtual.Status) {
			return virtual.DirectoryChild{}, virtual.StatusErrBadHandle
		}
	}
	root := d.root
	return func(r io.ByteReader) (virtual.DirectoryChild, virtual.Status) {
		var sb strings.Builder
		for {
			b, err := r.ReadByte()
			if err == io.EOF {
				break
			}
			if err != nil {
				return virtual.DirectoryChild{}, virtual.StatusErrBadHandle
			}
			sb.WriteByte(b)
		}
		rel := filepath.Clean(sb.String())
		// Reject handles that escape the root.
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return virtual.DirectoryChild{}, virtual.StatusErrBadHandle
		}
		hostPath := filepath.Join(root, rel)
		fi, err := os.Lstat(hostPath)
		if err != nil {
			return virtual.DirectoryChild{}, virtual.StatusErrStale
		}
		return childOf(nodeFor(hostPath, root, fi)), virtual.StatusOK
	}
}

// childOf wraps a node as a DirectoryChild.
func childOf(n virtual.Node) virtual.DirectoryChild {
	var c virtual.DirectoryChild
	if dir, ok := n.(virtual.Directory); ok {
		return c.FromDirectory(dir)
	}
	return c.FromLeaf(n.(virtual.Leaf))
}

// fileTypeOf maps an os.FileMode to a virtual.FileType.
func fileTypeOf(mode os.FileMode) virtual.FileType {
	switch {
	case mode&os.ModeDir != 0:
		return virtual.FileTypeDirectory
	case mode&os.ModeSymlink != 0:
		return virtual.FileTypeSymlink
	case mode&os.ModeNamedPipe != 0:
		return virtual.FileTypeFIFO
	case mode&os.ModeSocket != 0:
		return virtual.FileTypeSocket
	case mode&os.ModeCharDevice != 0:
		return virtual.FileTypeCharacterDevice
	case mode&os.ModeDevice != 0:
		return virtual.FileTypeBlockDevice
	case mode.IsRegular():
		return virtual.FileTypeRegularFile
	default:
		return virtual.FileTypeOther
	}
}

// fillAttributes populates the requested attributes from an os.FileInfo. It
// needs the node's host path and the FSAL root to compute the file handle.
func fillAttributes(fi os.FileInfo, hostPath, root string, requested virtual.AttributesMask, a *virtual.Attributes) {
	if requested&virtual.AttributesMaskFileType != 0 {
		a.SetFileType(fileTypeOf(fi.Mode()))
	}
	if requested&virtual.AttributesMaskPermissions != 0 {
		a.SetPermissions(virtual.NewPermissionsFromMode(uint32(fi.Mode().Perm())))
	}
	if requested&virtual.AttributesMaskSizeBytes != 0 {
		size := fi.Size()
		if size < 0 {
			size = 0
		}
		a.SetSizeBytes(uint64(size))
	}
	if requested&virtual.AttributesMaskLastDataModificationTime != 0 {
		a.SetLastDataModificationTime(fi.ModTime())
	}
	if requested&virtual.AttributesMaskFileHandle != 0 {
		a.SetFileHandle(osfsHandle(root, hostPath))
	}
	if requested&virtual.AttributesMaskHasNamedAttributes != 0 {
		a.SetHasNamedAttributes(false)
	}
	if requested&virtual.AttributesMaskIsInNamedAttributeDirectory != 0 {
		a.SetIsInNamedAttributeDirectory(false)
	}
	var ino uint64
	var nlink uint32 = 1
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
		nlink = uint32(st.Nlink)
	}
	if requested&virtual.AttributesMaskInodeNumber != 0 {
		a.SetInodeNumber(ino)
	}
	if requested&virtual.AttributesMaskLinkCount != 0 {
		a.SetLinkCount(nlink)
	}
	if requested&virtual.AttributesMaskChangeID != 0 {
		// Use the modification time (nanoseconds) as a change ID: it
		// advances whenever the file's data changes.
		a.SetChangeID(uint64(fi.ModTime().UnixNano()))
	}
}

// nodeFor builds the virtual node (directory or leaf) for a host path,
// given its already-resolved FileInfo, carrying the FSAL root for handles.
func nodeFor(path, root string, fi os.FileInfo) virtual.Node {
	if fi.IsDir() {
		return &directory{path: path, root: root}
	}
	return &file{path: path, root: root}
}

// --- directory ------------------------------------------------------------

type directory struct {
	path string
	root string
}

func (d *directory) VirtualGetAttributes(ctx context.Context, requested virtual.AttributesMask, attributes *virtual.Attributes) {
	fi, err := os.Stat(d.path)
	if err != nil {
		// A directory that vanished mid-traversal: report an empty,
		// type-only best effort.
		if requested&virtual.AttributesMaskFileType != 0 {
			attributes.SetFileType(virtual.FileTypeDirectory)
		}
		return
	}
	fillAttributes(fi, d.path, d.root, requested, attributes)
}

func (d *directory) VirtualSetAttributes(ctx context.Context, in *virtual.Attributes, requested virtual.AttributesMask, attributes *virtual.Attributes) virtual.Status {
	return virtual.StatusErrROFS
}

func (d *directory) VirtualApply(data any) bool { return false }

func (d *directory) VirtualOpenNamedAttributes(ctx context.Context, createDirectory bool, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Directory, virtual.Status) {
	return nil, virtual.StatusErrNoEnt
}

func (d *directory) VirtualLookup(ctx context.Context, name virtual.Component, requested virtual.AttributesMask, out *virtual.Attributes) (virtual.DirectoryChild, virtual.Status) {
	childPath := filepath.Join(d.path, name.String())
	fi, err := os.Lstat(childPath)
	if err != nil {
		if os.IsNotExist(err) {
			return virtual.DirectoryChild{}, virtual.StatusErrNoEnt
		}
		return virtual.DirectoryChild{}, virtual.StatusErrIO
	}
	node := nodeFor(childPath, d.root, fi)
	fillAttributes(fi, childPath, d.root, requested, out)
	return childOf(node), virtual.StatusOK
}

func (d *directory) VirtualReadDir(ctx context.Context, firstCookie uint64, requested virtual.AttributesMask, reporter virtual.DirectoryEntryReporter) virtual.Status {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return virtual.StatusErrIO
	}
	// os.ReadDir already returns entries sorted by name; cookies are
	// 1-based indices into that stable order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for i, entry := range entries {
		cookie := uint64(i + 1)
		if cookie <= firstCookie {
			continue
		}
		name, ok := virtual.NewComponent(entry.Name())
		if !ok {
			// Skip names that are not valid components (should not
			// happen for real on-disk names, but stay defensive).
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			continue
		}
		childPath := filepath.Join(d.path, entry.Name())
		node := nodeFor(childPath, d.root, fi)
		var attributes virtual.Attributes
		fillAttributes(fi, childPath, d.root, requested, &attributes)
		if !reporter.ReportEntry(cookie, name, childOf(node), &attributes) {
			break
		}
	}
	return virtual.StatusOK
}

func (d *directory) VirtualOpenChild(ctx context.Context, name virtual.Component, shareAccess virtual.ShareMask, createAttributes *virtual.Attributes, existingOptions *virtual.OpenExistingOptions, requested virtual.AttributesMask, openedFileAttributes *virtual.Attributes) (virtual.Leaf, virtual.AttributesMask, virtual.ChangeInfo, virtual.Status) {
	ci := virtual.ChangeInfo{}
	childPath := filepath.Join(d.path, name.String())
	fi, err := os.Stat(childPath)
	if err != nil {
		if os.IsNotExist(err) {
			if createAttributes != nil {
				return nil, 0, ci, virtual.StatusErrROFS
			}
			return nil, 0, ci, virtual.StatusErrNoEnt
		}
		return nil, 0, ci, virtual.StatusErrIO
	}
	if existingOptions == nil {
		return nil, 0, ci, virtual.StatusErrExist
	}
	if fi.IsDir() {
		return nil, 0, ci, virtual.StatusErrIsDir
	}
	if shareAccess&virtual.ShareMaskWrite != 0 {
		return nil, 0, ci, virtual.StatusErrROFS
	}
	leaf := &file{path: childPath, root: d.root}
	fillAttributes(fi, childPath, d.root, requested, openedFileAttributes)
	return leaf, 0, ci, virtual.StatusOK
}

func (d *directory) VirtualLink(ctx context.Context, name virtual.Component, leaf virtual.Leaf, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *directory) VirtualMkdir(name virtual.Component, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Directory, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *directory) VirtualMknod(ctx context.Context, name virtual.Component, fileType virtual.FileType, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Leaf, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *directory) VirtualRename(oldName virtual.Component, newDirectory virtual.Directory, newName virtual.Component) (virtual.ChangeInfo, virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *directory) VirtualRemove(name virtual.Component, removeDirectory, removeLeaf bool) (virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *directory) VirtualSymlink(ctx context.Context, pointedTo virtual.Parser, linkName virtual.Component, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Leaf, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrROFS
}

// --- file -----------------------------------------------------------------

type file struct {
	path string
	root string
}

func (f *file) VirtualGetAttributes(ctx context.Context, requested virtual.AttributesMask, attributes *virtual.Attributes) {
	fi, err := os.Lstat(f.path)
	if err != nil {
		if requested&virtual.AttributesMaskFileType != 0 {
			attributes.SetFileType(virtual.FileTypeRegularFile)
		}
		return
	}
	fillAttributes(fi, f.path, f.root, requested, attributes)
}

func (f *file) VirtualSetAttributes(ctx context.Context, in *virtual.Attributes, requested virtual.AttributesMask, attributes *virtual.Attributes) virtual.Status {
	return virtual.StatusErrROFS
}

func (f *file) VirtualApply(data any) bool { return false }

func (f *file) VirtualOpenNamedAttributes(ctx context.Context, createDirectory bool, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Directory, virtual.Status) {
	return nil, virtual.StatusErrNoEnt
}

func (f *file) VirtualAllocate(off, size uint64) virtual.Status { return virtual.StatusErrROFS }

func (f *file) VirtualSeek(offset uint64, regionType virtual.RegionType) (*uint64, virtual.Status) {
	fi, err := os.Stat(f.path)
	if err != nil {
		return nil, virtual.StatusErrIO
	}
	size := uint64(fi.Size())
	if offset >= size {
		return nil, virtual.StatusErrNXIO
	}
	switch regionType {
	case virtual.Data:
		o := offset
		return &o, virtual.StatusOK
	case virtual.Hole:
		return &size, virtual.StatusOK
	default:
		return nil, virtual.StatusErrInval
	}
}

func (f *file) VirtualOpenSelf(ctx context.Context, shareAccess virtual.ShareMask, options *virtual.OpenExistingOptions, requested virtual.AttributesMask, attributes *virtual.Attributes) virtual.Status {
	if shareAccess&virtual.ShareMaskWrite != 0 {
		return virtual.StatusErrROFS
	}
	fi, err := os.Stat(f.path)
	if err != nil {
		return virtual.StatusErrIO
	}
	fillAttributes(fi, f.path, f.root, requested, attributes)
	return virtual.StatusOK
}

func (f *file) VirtualRead(buf []byte, offset uint64) (int, bool, virtual.Status) {
	fh, err := os.Open(f.path)
	if err != nil {
		return 0, false, virtual.StatusErrIO
	}
	defer fh.Close()
	n, err := fh.ReadAt(buf, int64(offset))
	if err != nil && err != io.EOF {
		return 0, false, virtual.StatusErrIO
	}
	return n, err == io.EOF, virtual.StatusOK
}

func (f *file) VirtualClose(shareAccess virtual.ShareMask) {}

func (f *file) VirtualWrite(buf []byte, offset uint64) (int, virtual.Status) {
	return 0, virtual.StatusErrROFS
}
