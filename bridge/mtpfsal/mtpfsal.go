//go:build galatea

// Package mtpfsal implements Galatea's FSAL (github.com/terraceonhigh/galatea
// /pkg/virtual: Directory / Leaf / Node) over a live MTP session, so an Android
// phone's object store can be served as a userspace NFSv4 volume by
// galatea.Serve — replacing the patched willscott/go-nfs substrate in
// bridge/nfs and, with it, the JUKEBOX/prefetch machinery in bridge/nfs/cache.go
// that existed only to dodge NFSv3's RPC-timeout window. Galatea's NFSv4 floor
// tolerates multi-minute reads, so that workaround is retired (proven: Galatea
// R1, a 130 s READ exit-0; R7, a 1 GB round-trip byte-identical).
//
// # Phase 4 — first dry-fit
//
// This is the compiling skeleton (cf. Correspondance 04, "phase four and the
// one-cursor"). The read/navigation path (attributes, lookup, handle
// resolution) is wired against the in-memory ObjectMap; the MTP-touching data
// and mutation ops are stubbed with a pointer to the bridge/nfs logic they
// port. The interface assertions at the bottom are the contract receipt — if
// pkg/virtual moves, this file stops compiling.
//
// # The one-cursor boundary (load-bearing)
//
// Galatea calls the FSAL concurrently, from many goroutines, across NFSv4
// open-owners (see Galatea Correspondance 03). libmtp is NOT thread-safe:
// Comprador funnels every device operation through a single session-owning
// goroutine reached via (*mtp.Session).Do, which others call and block on.
// Therefore every Virtual* method here that touches the device MUST go through
// session.Do — never call libmtp directly. Attribute/lookup reads below hit
// only the in-memory ObjectMap (mutex-guarded, no device I/O), so they need no
// marshalling; the data ops do, and say so.
package mtpfsal

import (
	"context"
	"encoding/binary"
	"io"
	"path"
	"time"

	"github.com/terraceonhigh/galatea/pkg/virtual"

	"comprador/bridge/mtp"
)

// node is the shared state of every MTP-backed FSAL node: the session it
// belongs to and the object's path in ObjectMap form (leading slash, "/" root).
// Concrete Directory/Leaf identity is the object's MTP handle (ID); the path is
// carried for ObjectMap lookups and child-path construction.
type node struct {
	session *mtp.Session
	mpath   string // ObjectMap path: "/", "/DCIM", "/DCIM/Camera/IMG_0001.JPG"
}

// mtpDir is a virtual.Directory backed by an MTP folder object (or a storage
// root). The map root ("/") is also an mtpDir, synthesised in Root.
type mtpDir struct{ node }

// mtpFile is a virtual.Leaf backed by an MTP file object.
type mtpFile struct{ node }

// Root returns the FSAL root Directory for the given session, plus the
// HandleResolver galatea.Serve needs to turn a PUTFH handle back into a node.
// The root is the synthetic "/" directory above all MTP storages (multi-storage
// support presents each storage as a child — Phase 5 of CLAUDE.md; here the map
// already flattens them under "/").
func Root(session *mtp.Session) (virtual.Directory, virtual.HandleResolver) {
	return &mtpDir{node{session: session, mpath: "/"}}, newHandleResolver(session)
}

// ---- handles -------------------------------------------------------------
//
// MTP hands us a native uint32 object ID per object — far under NFS4_FHSIZE
// (~128 B), so unlike osfs's path-relative handles there is no length ceiling
// and no inode/hash scheme to invent (Correspondance 04, question 2). The
// handle is the 4-byte big-endian object ID; the synthetic root encodes as ID
// 0 (no MTP object has ID 0).

const rootHandleID uint32 = 0

func encodeHandle(id uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, id)
	return b
}

func newHandleResolver(session *mtp.Session) virtual.HandleResolver {
	return func(r io.ByteReader) (virtual.DirectoryChild, virtual.Status) {
		var b [4]byte
		for i := range b {
			c, err := r.ReadByte()
			if err != nil {
				return virtual.DirectoryChild{}, virtual.StatusErrBadHandle
			}
			b[i] = c
		}
		id := binary.BigEndian.Uint32(b[:])
		if id == rootHandleID {
			root := &mtpDir{node{session: session, mpath: "/"}}
			return virtual.DirectoryChild{}.FromDirectory(root), virtual.StatusOK
		}
		meta, ok := session.Objects.GetByID(id)
		if !ok {
			return virtual.DirectoryChild{}, virtual.StatusErrStale
		}
		return childFor(session, meta), virtual.StatusOK
	}
}

// childFor wraps an ObjectMeta as the appropriate DirectoryChild.
func childFor(session *mtp.Session, meta *mtp.ObjectMeta) virtual.DirectoryChild {
	n := node{session: session, mpath: meta.Path}
	if meta.IsDir {
		return virtual.DirectoryChild{}.FromDirectory(&mtpDir{n})
	}
	return virtual.DirectoryChild{}.FromLeaf(&mtpFile{n})
}

// handleID returns the object ID this node's path resolves to (rootHandleID for
// the synthetic root).
func (n node) handleID() uint32 {
	if n.mpath == "/" {
		return rootHandleID
	}
	if meta, ok := n.session.Objects.GetByPath(n.mpath); ok {
		return meta.ID
	}
	return rootHandleID
}

// ---- attributes ----------------------------------------------------------

// fillCommon sets the attributes the NFSv4 server treats as mandatory (file
// handle, named-attribute flags — see Galatea MISTAKES.md M-006) plus the
// requested stat-like fields, from an ObjectMeta. Reads the in-memory map only.
func fillCommon(meta *mtp.ObjectMeta, id uint32, requested virtual.AttributesMask, a *virtual.Attributes) {
	// Mandatory regardless of the requested mask.
	a.SetFileHandle(encodeHandle(id))
	a.SetHasNamedAttributes(false)
	a.SetIsInNamedAttributeDirectory(false)

	if requested&virtual.AttributesMaskInodeNumber != 0 {
		a.SetInodeNumber(uint64(id))
	}
	if requested&virtual.AttributesMaskLastDataModificationTime != 0 {
		mt := meta.ModTime
		if mt.IsZero() {
			mt = time.Unix(0, 0)
		}
		a.SetLastDataModificationTime(mt)
	}
}

func (d *mtpDir) VirtualGetAttributes(ctx context.Context, requested virtual.AttributesMask, a *virtual.Attributes) {
	id := d.handleID()
	var meta mtp.ObjectMeta
	if m, ok := d.session.Objects.GetByPath(d.mpath); ok {
		meta = *m
	} else {
		meta = mtp.ObjectMeta{ID: id, Path: d.mpath, IsDir: true}
	}
	if requested&virtual.AttributesMaskFileType != 0 {
		a.SetFileType(virtual.FileTypeDirectory)
	}
	if requested&virtual.AttributesMaskPermissions != 0 {
		a.SetPermissions(virtual.PermissionsRead | virtual.PermissionsWrite | virtual.PermissionsExecute)
	}
	if requested&virtual.AttributesMaskLinkCount != 0 {
		a.SetLinkCount(virtual.EmptyDirectoryLinkCount)
	}
	fillCommon(&meta, id, requested, a)
}

func (f *mtpFile) VirtualGetAttributes(ctx context.Context, requested virtual.AttributesMask, a *virtual.Attributes) {
	meta, ok := f.session.Objects.GetByPath(f.mpath)
	if !ok {
		// Vanished mid-traversal: type-only best effort.
		if requested&virtual.AttributesMaskFileType != 0 {
			a.SetFileType(virtual.FileTypeRegularFile)
		}
		return
	}
	if requested&virtual.AttributesMaskFileType != 0 {
		a.SetFileType(virtual.FileTypeRegularFile)
	}
	if requested&virtual.AttributesMaskPermissions != 0 {
		a.SetPermissions(virtual.PermissionsRead | virtual.PermissionsWrite)
	}
	if requested&virtual.AttributesMaskSizeBytes != 0 {
		a.SetSizeBytes(meta.Size)
	}
	if requested&virtual.AttributesMaskLinkCount != 0 {
		a.SetLinkCount(1)
	}
	fillCommon(meta, meta.ID, requested, a)
}

// VirtualSetAttributes — TODO(phase4): map size truncation onto the staged-write
// path (bridge/nfs/write.go) and mtime onto MTP if supported; reject the rest.
// For the dry-fit, accept no-ops so a stat round-trip doesn't error.
func (n *node) VirtualSetAttributes(ctx context.Context, in *virtual.Attributes, requested virtual.AttributesMask, out *virtual.Attributes) virtual.Status {
	return virtual.StatusErrInval
}

// VirtualApply: no host-defined extension payloads.
func (n *node) VirtualApply(data any) bool { return false }

// VirtualOpenNamedAttributes: MTP has no xattr/named-attribute concept.
func (n *node) VirtualOpenNamedAttributes(ctx context.Context, createDirectory bool, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Directory, virtual.Status) {
	return nil, virtual.StatusErrNotDir
}

// ---- directory navigation ------------------------------------------------

// VirtualLookup resolves a child by name via the in-memory ObjectMap. Mirrors
// bridge/nfs/fs.go Stat/Lstat. EnsurePopulated lazily fills the directory's
// children on first touch (a single MTP enumeration, serialised in session.Do).
func (d *mtpDir) VirtualLookup(ctx context.Context, name virtual.Component, requested virtual.AttributesMask, out *virtual.Attributes) (virtual.DirectoryChild, virtual.Status) {
	d.session.EnsurePopulated(d.mpath)
	childPath := path.Join(d.mpath, name.String())
	meta, ok := d.session.Objects.GetByPath(childPath)
	if !ok {
		return virtual.DirectoryChild{}, virtual.StatusErrNoEnt
	}
	child := childFor(d.session, meta)
	if dir, leaf := child.GetPair(); dir != nil {
		dir.VirtualGetAttributes(ctx, requested, out)
	} else if leaf != nil {
		leaf.VirtualGetAttributes(ctx, requested, out)
	}
	return child, virtual.StatusOK
}

// VirtualReadDir — TODO(phase4): enumerate children. ObjectMap needs a
// children-of-path index (populateDir returns []*ObjectMeta; expose it) before
// this can stream entries to the reporter with cookies. Mirrors fs.go ReadDir.
func (d *mtpDir) VirtualReadDir(ctx context.Context, firstCookie uint64, requested virtual.AttributesMask, reporter virtual.DirectoryEntryReporter) virtual.Status {
	return virtual.StatusErrIO
}

// ---- data ops (must marshal through session.Do — see package doc) ---------

// VirtualOpenSelf — TODO(phase4): MTP has no open; record the share mode and
// validate existence. Cf. fs.go Open/OpenFile.
func (f *mtpFile) VirtualOpenSelf(ctx context.Context, shareAccess virtual.ShareMask, options *virtual.OpenExistingOptions, requested virtual.AttributesMask, attributes *virtual.Attributes) virtual.Status {
	return virtual.StatusErrIO
}

// VirtualRead — TODO(phase4): the heart of the win. Port fs.go's read onto a
// session.Do request that streams libmtp GetFileToHandler at the given offset.
// NFSv4 tolerates the multi-minute read JUKEBOX existed to dodge — no threshold,
// no prefetch goroutine, no cache.go.
func (f *mtpFile) VirtualRead(buf []byte, offset uint64) (int, bool, virtual.Status) {
	return 0, false, virtual.StatusErrIO
}

// VirtualWrite — TODO(phase4): port the staged-write path (write.go writeRegistry
// + idle-flush commit). MTP has no partial write; stage to a temp file and
// SendFileFromReader on close/flush.
func (f *mtpFile) VirtualWrite(buf []byte, offset uint64) (int, virtual.Status) {
	return 0, virtual.StatusErrIO
}

func (f *mtpFile) VirtualClose(shareAccess virtual.ShareMask) {}

// VirtualAllocate / VirtualSeek — MTP has neither fallocate nor sparse seeking.
func (f *mtpFile) VirtualAllocate(off, size uint64) virtual.Status { return virtual.StatusErrInval }
func (f *mtpFile) VirtualSeek(offset uint64, regionType virtual.RegionType) (*uint64, virtual.Status) {
	return nil, virtual.StatusErrInval
}

// ---- mutation ops (copy+delete for rename — MTP has no native rename) ------
//
// All TODO(phase4); each mirrors the correspondingly-named bridge/nfs handler
// and must run through session.Do.

func (d *mtpDir) VirtualOpenChild(ctx context.Context, name virtual.Component, shareAccess virtual.ShareMask, createAttributes *virtual.Attributes, existingOptions *virtual.OpenExistingOptions, requested virtual.AttributesMask, openedFileAttributes *virtual.Attributes) (virtual.Leaf, virtual.AttributesMask, virtual.ChangeInfo, virtual.Status) {
	return nil, 0, virtual.ChangeInfo{}, virtual.StatusErrIO
}

func (d *mtpDir) VirtualMkdir(name virtual.Component, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Directory, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrIO
}

func (d *mtpDir) VirtualMknod(ctx context.Context, name virtual.Component, fileType virtual.FileType, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Leaf, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrInval
}

func (d *mtpDir) VirtualRemove(name virtual.Component, removeDirectory, removeLeaf bool) (virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.StatusErrIO
}

func (d *mtpDir) VirtualRename(oldName virtual.Component, newDirectory virtual.Directory, newName virtual.Component) (virtual.ChangeInfo, virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.ChangeInfo{}, virtual.StatusErrIO
}

func (d *mtpDir) VirtualLink(ctx context.Context, name virtual.Component, leaf virtual.Leaf, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.StatusErrInval
}

func (d *mtpDir) VirtualSymlink(ctx context.Context, pointedTo virtual.Parser, linkName virtual.Component, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Leaf, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrInval
}

// ---- contract receipt ----------------------------------------------------

var (
	_ virtual.Directory = (*mtpDir)(nil)
	_ virtual.Leaf      = (*mtpFile)(nil)
)
