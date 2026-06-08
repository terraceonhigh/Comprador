package virtual

import (
	"context"
	"encoding/binary"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// NewMemoryHandleResolver builds a HandleResolver (DEC-017 Option B) over the
// in-memory tree rooted at root, which must be an in-memory directory. It walks
// the tree once, indexing every node by its inode, and returns a resolver that
// decodes the 8-byte inode handle (see memoryFileHandle) back to its node. The
// NFSv4 server needs this so a client's PUTFH — replaying a handle it obtained
// from an earlier GETFH — resolves to the right node.
//
// It resolves by walking the *live* tree on each call, so nodes created after
// construction (mkdir, file create — R6b) resolve and removed nodes return
// stale. A snapshot index would miss them (it did: rmdir of a freshly-created
// dir failed with ESTALE because the client PUTFH'd the new dir's handle). The
// walk is O(tree) per resolve — fine for this in-memory test/demo FSAL; a real
// backend would keep a live index.
func NewMemoryHandleResolver(root Directory) HandleResolver {
	return func(r io.ByteReader) (DirectoryChild, Status) {
		var h [8]byte
		for i := range h {
			b, err := r.ReadByte()
			if err != nil {
				return DirectoryChild{}, StatusErrBadHandle
			}
			h[i] = b
		}
		if n := findByInode(root, binary.BigEndian.Uint64(h[:])); n != nil {
			return childOf(n), StatusOK
		}
		return DirectoryChild{}, StatusErrStale
	}
}

// findByInode searches the live tree rooted at n for the node with the given
// inode. It snapshots each directory's children under that directory's lock,
// then recurses without holding it (so it never nests directory locks).
func findByInode(n Node, target uint64) Node {
	switch t := n.(type) {
	case *memoryFile:
		if t.inode == target {
			return t
		}
	case *memoryDirectory:
		if t.inode == target {
			return t
		}
		t.mu.Lock()
		kids := make([]Node, 0, len(t.children))
		for _, c := range t.children {
			kids = append(kids, c)
		}
		t.mu.Unlock()
		for _, c := range kids {
			if found := findByInode(c, target); found != nil {
				return found
			}
		}
	}
	return nil
}

// memoryFileHandle encodes a node's stable inode number as an 8-byte NFS file
// handle. This is the in-memory FSAL's half of DEC-017's Option B (backends
// self-assign handles): the server reads it via AttributesMaskFileHandle, and a
// resolver (handle → node) decodes the inode back. Big-endian so handles sort
// the way inodes do.
func memoryFileHandle(inode uint64) []byte {
	h := make([]byte, 8)
	binary.BigEndian.PutUint64(h, inode)
	return h
}

// This file is a minimal, read-only in-memory FSAL. It exists to prove
// the interface in pkg/virtual is implementable and exercisable without
// any backend — the trivial test FSAL DEC-005 and the charge letter
// (Phase 1) call for before a real backend or the lifted NFSv4 server
// exist. Mutating operations return StatusErrROFS; reads work.

// childOf builds a DirectoryChild from a Node, dispatching on whether it
// is a Directory or a Leaf.
func childOf(n Node) DirectoryChild {
	var c DirectoryChild
	if d, ok := n.(Directory); ok {
		return c.FromDirectory(d)
	}
	if l, ok := n.(Leaf); ok {
		return c.FromLeaf(l)
	}
	panic("node is neither a Directory nor a Leaf")
}

// --- memoryFile: a writable regular file ----------------------------------

// Synthetic filesystem-wide statfs values for the in-memory FSAL. Deliberately
// distinct so a SETATTR/GETATTR round-trip test can assert each lands in its own
// slot (no cross-wiring), and large enough that tools see a real, writable volume.
const (
	memSpaceTotal = uint64(1) << 30 // 1 GiB
	memSpaceFree  = uint64(1) << 29 // 512 MiB
	memSpaceAvail = uint64(1) << 28 // 256 MiB
	memFilesTotal = uint64(1_000_000)
	memFilesFree  = uint64(600_000)
	memFilesAvail = uint64(500_000)
)

// fillSyntheticStatfs answers the filesystem-wide space/inode attributes for the
// in-memory FSAL. Shared by memoryFile and memoryDirectory (statfs is per-fs, so
// every node reports the same values).
func fillSyntheticStatfs(requested AttributesMask, a *Attributes) {
	if requested&AttributesMaskSpaceTotal != 0 {
		a.SetSpaceTotal(memSpaceTotal)
	}
	if requested&AttributesMaskSpaceFree != 0 {
		a.SetSpaceFree(memSpaceFree)
	}
	if requested&AttributesMaskSpaceAvail != 0 {
		a.SetSpaceAvail(memSpaceAvail)
	}
	if requested&AttributesMaskFilesTotal != 0 {
		a.SetFilesTotal(memFilesTotal)
	}
	if requested&AttributesMaskFilesFree != 0 {
		a.SetFilesFree(memFilesFree)
	}
	if requested&AttributesMaskFilesAvail != 0 {
		a.SetFilesAvail(memFilesAvail)
	}
}

// memoryFile is an in-memory regular file. Its contents and size are mutable
// (R6 write path); a per-file mutex serializes reads, writes, truncations, and
// attribute reads of this file. Directory structure is a separate concern (the
// directory's own concurrency); a memoryFile only guards its own bytes.
type memoryFile struct {
	mu        sync.Mutex
	inode     uint64
	perms     Permissions
	contents  []byte
	atime     time.Time     // last access time; set via SETATTR time_access_set
	mtime     time.Time     // last data modification time; set via SETATTR time_modify_set
	readDelay time.Duration // if >0, VirtualRead sleeps this long first
}

// NewMemoryFile returns an in-memory regular file with the given initial
// contents. The file is writable (see VirtualWrite / VirtualSetAttributes).
func NewMemoryFile(inode uint64, perms Permissions, contents []byte) Leaf {
	return &memoryFile{inode: inode, perms: perms, contents: contents}
}

// NewSlowMemoryFile is NewMemoryFile whose every VirtualRead sleeps for delay
// first — a backend whose read of one object is slow (e.g. a multi-minute
// libmtp fetch). It stays a *memoryFile (so the handle resolver finds it); the
// R1 probe uses it to test whether the macOS NFSv4 client tolerates a single
// READ RPC longer than the NFSv3 RPC-timeout window.
func NewSlowMemoryFile(inode uint64, perms Permissions, contents []byte, delay time.Duration) Leaf {
	return &memoryFile{inode: inode, perms: perms, contents: contents, readDelay: delay}
}

// fillAttributes writes the requested attributes. The caller must hold f.mu
// (it reads mutable size/perms).
func (f *memoryFile) fillAttributes(requested AttributesMask, a *Attributes) {
	if requested&AttributesMaskFileType != 0 {
		a.SetFileType(FileTypeRegularFile)
	}
	if requested&AttributesMaskPermissions != 0 {
		a.SetPermissions(f.perms)
	}
	if requested&AttributesMaskSizeBytes != 0 {
		a.SetSizeBytes(uint64(len(f.contents)))
	}
	if requested&AttributesMaskInodeNumber != 0 {
		a.SetInodeNumber(f.inode)
	}
	if requested&AttributesMaskFileHandle != 0 {
		a.SetFileHandle(memoryFileHandle(f.inode))
	}
	if requested&AttributesMaskHasNamedAttributes != 0 {
		a.SetHasNamedAttributes(false)
	}
	if requested&AttributesMaskIsInNamedAttributeDirectory != 0 {
		// This FSAL has no named-attribute directories.
		a.SetIsInNamedAttributeDirectory(false)
	}
	if requested&AttributesMaskLinkCount != 0 {
		a.SetLinkCount(1)
	}
	if requested&AttributesMaskChangeID != 0 {
		a.SetChangeID(0)
	}
	if requested&AttributesMaskLastDataModificationTime != 0 && !f.mtime.IsZero() {
		a.SetLastDataModificationTime(f.mtime)
	}
	if requested&AttributesMaskLastAccessTime != 0 && !f.atime.IsZero() {
		a.SetLastAccessTime(f.atime)
	}
	fillSyntheticStatfs(requested, a)
}

func (f *memoryFile) VirtualGetAttributes(ctx context.Context, requested AttributesMask, attributes *Attributes) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fillAttributes(requested, attributes)
}

// resize truncates or zero-extends the contents to size. Caller holds f.mu.
func (f *memoryFile) resize(size uint64) {
	switch n := uint64(len(f.contents)); {
	case size < n:
		f.contents = f.contents[:size]
	case size > n:
		f.contents = append(f.contents, make([]byte, size-n)...)
	}
}

func (f *memoryFile) VirtualSetAttributes(ctx context.Context, in *Attributes, requested AttributesMask, attributes *Attributes) Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Apply whatever `in` carries (its own fieldsPresent says which) — NOT what
	// `requested` asks to read back. Keying off `requested` was a bug: the macOS
	// client sends SETATTR(size=0) for `>` but requests a different return mask,
	// so the truncate was silently skipped (found by tracing a live mount).
	if size, ok := in.GetSizeBytes(); ok {
		f.resize(size)
	}
	if perms, ok := in.GetPermissions(); ok {
		f.perms = perms
	}
	if mtime, ok := in.GetLastDataModificationTime(); ok {
		f.mtime = mtime
	}
	if atime, ok := in.GetLastAccessTime(); ok {
		f.atime = atime
	}
	f.fillAttributes(requested, attributes)
	return StatusOK
}

func (f *memoryFile) VirtualApply(data any) bool { return false }

func (f *memoryFile) VirtualOpenNamedAttributes(ctx context.Context, createDirectory bool, requested AttributesMask, attributes *Attributes) (Directory, Status) {
	return nil, StatusErrNoEnt
}

func (f *memoryFile) VirtualAllocate(off, size uint64) Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if end := off + size; end > uint64(len(f.contents)) {
		f.resize(end)
	}
	return StatusOK
}

func (f *memoryFile) VirtualSeek(offset uint64, regionType RegionType) (*uint64, Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	size := uint64(len(f.contents))
	if offset >= size {
		return nil, StatusErrNXIO
	}
	switch regionType {
	case Data:
		// The whole file is data; the next data region starts here.
		o := offset
		return &o, StatusOK
	case Hole:
		// No holes; the only "hole" is the implicit one at EOF.
		return &size, StatusOK
	default:
		return nil, StatusErrInval
	}
}

func (f *memoryFile) VirtualOpenSelf(ctx context.Context, shareAccess ShareMask, options *OpenExistingOptions, requested AttributesMask, attributes *Attributes) Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if options != nil && options.Truncate {
		// Open-with-truncate (e.g. shell `>`): empty the file first.
		f.resize(0)
	}
	f.fillAttributes(requested, attributes)
	return StatusOK
}

func (f *memoryFile) VirtualRead(buf []byte, offset uint64) (int, bool, Status) {
	if f.readDelay > 0 {
		// Simulate a slow backend fetch — sleep before taking the lock so
		// only this read is delayed, not concurrent ops on the file.
		time.Sleep(f.readDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	size := uint64(len(f.contents))
	if offset >= size {
		// At or past end-of-file: nothing to read. An NFS client (and
		// pjdfstest) will issue such reads; they must report EOF, not
		// slice out of range.
		return 0, true, StatusOK
	}
	data, eof := BoundReadToFileSize(buf, offset, size)
	n := copy(data, f.contents[offset:])
	return n, eof, StatusOK
}

func (f *memoryFile) VirtualClose(shareAccess ShareMask) {}

// VirtualWrite writes buf at offset, zero-extending the file first if the write
// starts past the current end.
func (f *memoryFile) VirtualWrite(buf []byte, offset uint64) (int, Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if end := offset + uint64(len(buf)); end > uint64(len(f.contents)) {
		f.resize(end)
	}
	copy(f.contents[offset:], buf)
	return len(buf), StatusOK
}

// --- memoryDirectory --------------------------------------------------------

// memoryDirectory is an in-memory directory. Created via
// NewWritableMemoryDirectory it is mutable (R6b): create/mkdir/remove add and
// drop children under the directory's own mutex, with inodes drawn from a
// counter shared across the tree (so every node gets a unique file handle).
// The legacy NewMemoryDirectory constructor leaves nextInode nil — that tree is
// read-only (mutations return ROFS), as the read-only fixtures and the original
// demo expect. A per-directory lock (not a tree-wide one) is used so a parent's
// readdir can read a child's attributes without re-entering the same mutex.
type memoryDirectory struct {
	mu        sync.Mutex
	inode     uint64
	perms     Permissions
	atime     time.Time // last access time; set via SETATTR time_access_set
	mtime     time.Time // last modification time; set via SETATTR time_modify_set
	children  map[string]Node
	nextInode *atomic.Uint64 // non-nil => writable; shared across the tree
}

// NewMemoryDirectory returns a read-only in-memory directory. Each value
// in children must itself be a *memoryFile or *memoryDirectory (i.e. a
// Leaf or a Directory).
func NewMemoryDirectory(inode uint64, perms Permissions, children map[string]Node) Directory {
	if children == nil {
		children = map[string]Node{}
	}
	return &memoryDirectory{inode: inode, perms: perms, children: children}
}

// NewWritableMemoryDirectory returns an empty, writable in-memory directory
// (inode 1). Files and subdirectories created beneath it, recursively, share a
// tree-wide inode counter so each receives a unique file handle.
func NewWritableMemoryDirectory(perms Permissions) Directory {
	counter := &atomic.Uint64{}
	counter.Store(1) // root is inode 1; the first created child is 2
	return &memoryDirectory{
		inode:     1,
		perms:     perms,
		children:  map[string]Node{},
		nextInode: counter,
	}
}

func (d *memoryDirectory) writable() bool { return d.nextInode != nil }

func (d *memoryDirectory) fillAttributes(requested AttributesMask, a *Attributes) {
	if requested&AttributesMaskFileType != 0 {
		a.SetFileType(FileTypeDirectory)
	}
	if requested&AttributesMaskPermissions != 0 {
		a.SetPermissions(d.perms)
	}
	if requested&AttributesMaskSizeBytes != 0 {
		a.SetSizeBytes(0)
	}
	if requested&AttributesMaskInodeNumber != 0 {
		a.SetInodeNumber(d.inode)
	}
	if requested&AttributesMaskFileHandle != 0 {
		a.SetFileHandle(memoryFileHandle(d.inode))
	}
	if requested&AttributesMaskHasNamedAttributes != 0 {
		a.SetHasNamedAttributes(false)
	}
	if requested&AttributesMaskIsInNamedAttributeDirectory != 0 {
		// This FSAL has no named-attribute directories.
		a.SetIsInNamedAttributeDirectory(false)
	}
	if requested&AttributesMaskLinkCount != 0 {
		a.SetLinkCount(EmptyDirectoryLinkCount)
	}
	if requested&AttributesMaskChangeID != 0 {
		a.SetChangeID(0)
	}
	if requested&AttributesMaskLastDataModificationTime != 0 && !d.mtime.IsZero() {
		a.SetLastDataModificationTime(d.mtime)
	}
	if requested&AttributesMaskLastAccessTime != 0 && !d.atime.IsZero() {
		a.SetLastAccessTime(d.atime)
	}
	fillSyntheticStatfs(requested, a)
}

func (d *memoryDirectory) VirtualGetAttributes(ctx context.Context, requested AttributesMask, attributes *Attributes) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fillAttributes(requested, attributes)
}

func (d *memoryDirectory) VirtualSetAttributes(ctx context.Context, in *Attributes, requested AttributesMask, attributes *Attributes) Status {
	if !d.writable() {
		return StatusErrROFS
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if perms, ok := in.GetPermissions(); ok {
		d.perms = perms
	}
	if mtime, ok := in.GetLastDataModificationTime(); ok {
		d.mtime = mtime
	}
	if atime, ok := in.GetLastAccessTime(); ok {
		d.atime = atime
	}
	d.fillAttributes(requested, attributes)
	return StatusOK
}

func (d *memoryDirectory) VirtualApply(data any) bool { return false }

func (d *memoryDirectory) VirtualOpenNamedAttributes(ctx context.Context, createDirectory bool, requested AttributesMask, attributes *Attributes) (Directory, Status) {
	return nil, StatusErrNoEnt
}

func (d *memoryDirectory) VirtualOpenChild(ctx context.Context, name Component, shareAccess ShareMask, createAttributes *Attributes, existingOptions *OpenExistingOptions, requested AttributesMask, openedFileAttributes *Attributes) (Leaf, AttributesMask, ChangeInfo, Status) {
	ci := ChangeInfo{}
	d.mu.Lock()
	child, ok := d.children[name.String()]
	if !ok {
		if createAttributes != nil {
			if !d.writable() {
				d.mu.Unlock()
				return nil, 0, ci, StatusErrROFS
			}
			// Create a new regular file.
			perms := PermissionsRead | PermissionsWrite
			if p, ok := createAttributes.GetPermissions(); ok {
				perms = p
			}
			f := &memoryFile{inode: d.nextInode.Add(1), perms: perms}
			d.children[name.String()] = f
			d.mu.Unlock()
			s := f.VirtualOpenSelf(ctx, shareAccess, &OpenExistingOptions{}, requested, openedFileAttributes)
			return f, 0, ci, s
		}
		d.mu.Unlock()
		return nil, 0, ci, StatusErrNoEnt
	}
	d.mu.Unlock()
	if existingOptions == nil {
		// Caller asked to create-only, but the file exists.
		return nil, 0, ci, StatusErrExist
	}
	leaf, ok := child.(Leaf)
	if !ok {
		return nil, 0, ci, StatusErrIsDir
	}
	if s := leaf.VirtualOpenSelf(ctx, shareAccess, existingOptions, requested, openedFileAttributes); s != StatusOK {
		return nil, 0, ci, s
	}
	return leaf, 0, ci, StatusOK
}

func (d *memoryDirectory) VirtualLink(ctx context.Context, name Component, leaf Leaf, requested AttributesMask, attributes *Attributes) (ChangeInfo, Status) {
	return ChangeInfo{}, StatusErrROFS
}

func (d *memoryDirectory) VirtualLookup(ctx context.Context, name Component, requested AttributesMask, out *Attributes) (DirectoryChild, Status) {
	d.mu.Lock()
	child, ok := d.children[name.String()]
	d.mu.Unlock()
	if !ok {
		return DirectoryChild{}, StatusErrNoEnt
	}
	child.VirtualGetAttributes(ctx, requested, out)
	return childOf(child), StatusOK
}

func (d *memoryDirectory) VirtualMkdir(name Component, requested AttributesMask, attributes *Attributes) (Directory, ChangeInfo, Status) {
	ci := ChangeInfo{}
	if !d.writable() {
		return nil, ci, StatusErrROFS
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.children[name.String()]; ok {
		return nil, ci, StatusErrExist
	}
	child := &memoryDirectory{
		inode:     d.nextInode.Add(1),
		perms:     PermissionsRead | PermissionsWrite | PermissionsExecute,
		children:  map[string]Node{},
		nextInode: d.nextInode,
	}
	d.children[name.String()] = child
	child.fillAttributes(requested, attributes)
	return child, ci, StatusOK
}

func (d *memoryDirectory) VirtualMknod(ctx context.Context, name Component, fileType FileType, requested AttributesMask, attributes *Attributes) (Leaf, ChangeInfo, Status) {
	return nil, ChangeInfo{}, StatusErrROFS
}

func (d *memoryDirectory) VirtualReadDir(ctx context.Context, firstCookie uint64, requested AttributesMask, reporter DirectoryEntryReporter) Status {
	// Snapshot the children under the lock, then release it before calling
	// each child's VirtualGetAttributes (which takes the child's own lock) —
	// avoids holding d's lock across nested calls.
	d.mu.Lock()
	names := make([]string, 0, len(d.children))
	for name := range d.children {
		names = append(names, name)
	}
	sort.Strings(names)
	nodes := make([]Node, len(names))
	for i, name := range names {
		nodes[i] = d.children[name]
	}
	d.mu.Unlock()

	for i, name := range names {
		cookie := uint64(i + 1)
		if cookie <= firstCookie {
			continue
		}
		var attributes Attributes
		nodes[i].VirtualGetAttributes(ctx, requested, &attributes)
		component, ok := NewComponent(name)
		if !ok {
			return StatusErrIO
		}
		if !reporter.ReportEntry(cookie, component, childOf(nodes[i]), &attributes) {
			break
		}
	}
	return StatusOK
}

func (d *memoryDirectory) VirtualRename(oldName Component, newDirectory Directory, newName Component) (ChangeInfo, ChangeInfo, Status) {
	if !d.writable() {
		return ChangeInfo{}, ChangeInfo{}, StatusErrROFS
	}
	nd, ok := newDirectory.(*memoryDirectory)
	if !ok || nd.nextInode != d.nextInode {
		// Renaming across FSALs / trees is not supported.
		return ChangeInfo{}, ChangeInfo{}, StatusErrXDev
	}
	// Lock the source and destination directories. When they differ, lock in a
	// consistent order (lowest inode first) so concurrent renames can't deadlock.
	if d == nd {
		d.mu.Lock()
		defer d.mu.Unlock()
	} else {
		first, second := d, nd
		if first.inode > second.inode {
			first, second = second, first
		}
		first.mu.Lock()
		defer first.mu.Unlock()
		second.mu.Lock()
		defer second.mu.Unlock()
	}

	child, ok := d.children[oldName.String()]
	if !ok {
		return ChangeInfo{}, ChangeInfo{}, StatusErrNoEnt
	}
	// POSIX rename replaces an existing target. (A refinement — refusing to
	// replace a non-empty directory target — is skipped here: checking it means
	// locking a third directory while holding two, a deadlock risk; the client
	// typically pre-checks anyway. Noted for R6 hardening.)
	delete(d.children, oldName.String())
	nd.children[newName.String()] = child
	return ChangeInfo{}, ChangeInfo{}, StatusOK
}

func (d *memoryDirectory) VirtualRemove(name Component, removeDirectory, removeLeaf bool) (ChangeInfo, Status) {
	ci := ChangeInfo{}
	if !d.writable() {
		return ci, StatusErrROFS
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	child, ok := d.children[name.String()]
	if !ok {
		return ci, StatusErrNoEnt
	}
	if subdir, isDir := child.(*memoryDirectory); isDir {
		if !removeDirectory {
			return ci, StatusErrIsDir
		}
		// POSIX rmdir refuses a non-empty directory.
		subdir.mu.Lock()
		empty := len(subdir.children) == 0
		subdir.mu.Unlock()
		if !empty {
			return ci, StatusErrNotEmpty
		}
	} else if !removeLeaf {
		// A leaf where the caller intended to remove only a directory.
		return ci, StatusErrNotDir
	}
	delete(d.children, name.String())
	return ci, StatusOK
}

func (d *memoryDirectory) VirtualSymlink(ctx context.Context, pointedTo Parser, linkName Component, requested AttributesMask, attributes *Attributes) (Leaf, ChangeInfo, Status) {
	return nil, ChangeInfo{}, StatusErrROFS
}
