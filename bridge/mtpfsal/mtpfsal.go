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
	"log"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/terraceonhigh/galatea/pkg/virtual"

	"comprador/bridge/mtp"
	"comprador/bridge/staging"
)

// sliceWriter fills a fixed buffer, used to adapt the libmtp OpGetPartial
// io.Writer into NFSv4's read-into-buffer model. Writes past the buffer end
// report io.ErrShortWrite (libmtp shouldn't overrun a bounded request, but
// stay defensive).
type sliceWriter struct {
	buf []byte
	n   int
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	c := copy(w.buf[w.n:], p)
	w.n += c
	if c < len(p) {
		return c, io.ErrShortWrite
	}
	return c, nil
}

// node is the shared state of every MTP-backed FSAL node: the session it
// belongs to and the object's path in ObjectMap form (leading slash, "/" root).
// Concrete Directory/Leaf identity is the object's MTP handle (ID); the path is
// carried for ObjectMap lookups and child-path construction.
type node struct {
	session *mtp.Session
	reg     *staging.Registry // shared write-staging registry (nil-safe for reads)
	mpath   string            // ObjectMap path: "/", "/DCIM", "/DCIM/Camera/IMG_0001.JPG"
}

// mtpDir is a virtual.Directory backed by an MTP folder object (or a storage
// root). The map root ("/") is also an mtpDir, synthesised in Root.
type mtpDir struct{ node }

// mtpFile is a virtual.Leaf backed by an MTP file object, or — between OPEN and
// the idle commit — by a staging entry keyed on mpath in the registry. Which one
// a given method sees is decided by reg.Get(mpath): non-nil means the file is
// still staging (pre-commit), nil means it's a committed device object (or a
// plain read).
type mtpFile struct{ node }

// Root returns the FSAL root Directory for the given session, plus the
// HandleResolver galatea.Serve needs to turn a PUTFH handle back into a node.
// The root is the synthetic "/" directory above all MTP storages (multi-storage
// support presents each storage as a child — Phase 5 of CLAUDE.md; here the map
// already flattens them under "/").
func Root(session *mtp.Session) (virtual.Directory, virtual.HandleResolver) {
	var reg *staging.Registry
	// The idle-flush callback resolves one staged path. It closes over reg
	// (assigned just below — the closure runs much later) and runs on the timer
	// goroutine, off the server's request goroutines. AppleDouble sidecars
	// (`._*`, .DS_Store) are Finder bookkeeping that has no business on the
	// phone: we stage them so OPEN/WRITE/GETATTR don't error, then drop them
	// here instead of committing. Everything else flushes to the device.
	reg = staging.NewRegistry(func(mtpPath string) {
		resolveStaged(reg, session, mtpPath)
	})
	return &mtpDir{node{session: session, reg: reg, mpath: "/"}}, newHandleResolver(session, reg)
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

func newHandleResolver(session *mtp.Session, reg *staging.Registry) virtual.HandleResolver {
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
			root := &mtpDir{node{session: session, reg: reg, mpath: "/"}}
			return virtual.DirectoryChild{}.FromDirectory(root), virtual.StatusOK
		}
		// A staged file's synthetic handle resolves before the device map: in
		// the pre-commit window the object ID doesn't exist on the phone yet, so
		// GetByID would miss. (Synthetic IDs live at 1<<31+, above any real
		// Android object ID, so this can't shadow a device object.)
		if mpath, ok := reg.PathForHandle(id); ok {
			leaf := &mtpFile{node{session: session, reg: reg, mpath: mpath}}
			return virtual.DirectoryChild{}.FromLeaf(leaf), virtual.StatusOK
		}
		meta, ok := session.Objects.GetByID(id)
		if !ok {
			return virtual.DirectoryChild{}, virtual.StatusErrStale
		}
		return childFor(session, reg, meta), virtual.StatusOK
	}
}

// childFor wraps an ObjectMeta as the appropriate DirectoryChild.
func childFor(session *mtp.Session, reg *staging.Registry, meta *mtp.ObjectMeta) virtual.DirectoryChild {
	n := node{session: session, reg: reg, mpath: meta.Path}
	if meta.IsDir {
		return virtual.DirectoryChild{}.FromDirectory(&mtpDir{n})
	}
	return virtual.DirectoryChild{}.FromLeaf(&mtpFile{node: n})
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

// MTP exposes no inode/file counts, so statfs reports large synthetic file
// totals — enough that Finder never treats the volume as out of inodes. The
// byte figures, by contrast, are the device's real capacity (below).
const (
	statfsFilesTotal = uint64(10_000_000)
	statfsFilesFree  = uint64(9_000_000)
	statfsFilesAvail = uint64(9_000_000)
)

// fillStatfs answers the filesystem-wide space/inode attributes the NFSv4 client
// requests for statfs — what Finder reads to show "N bytes available" and to
// pre-flight a drag-and-drop copy. Without it the server encodes 0, Finder shows
// "Zero bytes available" and refuses every copy (the precursor to error 100060).
// Byte totals come from the live device storages (session.TotalBytes/FreeBytes,
// RLock-guarded — no session-goroutine round-trip); statfs is per-filesystem, so
// every node reports the same values (cf. Galatea's memory.go fillSyntheticStatfs).
//
// These are the storage snapshot taken at session open; they don't yet decrement
// live after a write (the willscott bridge called RefreshStorages on each FSStat
// for that — a follow-up if Finder's post-write number needs to be exact). For
// unblocking drag-and-drop, the real free figure is what matters and it's here.
func (n node) fillStatfs(requested virtual.AttributesMask, a *virtual.Attributes) {
	if requested&virtual.AttributesMaskSpaceTotal != 0 {
		a.SetSpaceTotal(n.session.TotalBytes())
	}
	if requested&virtual.AttributesMaskSpaceFree != 0 {
		a.SetSpaceFree(n.session.FreeBytes())
	}
	if requested&virtual.AttributesMaskSpaceAvail != 0 {
		// MTP has no per-user quota; available == free.
		a.SetSpaceAvail(n.session.FreeBytes())
	}
	if requested&virtual.AttributesMaskFilesTotal != 0 {
		a.SetFilesTotal(statfsFilesTotal)
	}
	if requested&virtual.AttributesMaskFilesFree != 0 {
		a.SetFilesFree(statfsFilesFree)
	}
	if requested&virtual.AttributesMaskFilesAvail != 0 {
		a.SetFilesAvail(statfsFilesAvail)
	}
}

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
	mt := meta.ModTime
	if mt.IsZero() {
		mt = time.Unix(0, 0)
	}
	if requested&virtual.AttributesMaskLastDataModificationTime != 0 {
		a.SetLastDataModificationTime(mt)
	}
	// ChangeID is mandatory-when-requested (the server panics otherwise — the
	// M-006 lesson). Derive it from the modification time, like osfs: it
	// advances whenever the object's data changes.
	if requested&virtual.AttributesMaskChangeID != 0 {
		a.SetChangeID(uint64(mt.UnixNano()))
	}
}

// staged returns the in-progress staging file for this leaf's path, or nil if
// the file is a committed device object (or reg is unset, as on a pure read).
func (f *mtpFile) staged() *staging.File {
	if f.reg == nil {
		return nil
	}
	return f.reg.Get(f.mpath)
}

// fillStaged answers GETATTR for a file that is still buffering to a temp file
// (post-OPEN, pre-commit). Size comes from the temp file so Finder sees the
// upload grow; the handle is the registry's synthetic ID (stable for the staged
// life); ChangeID is the registry's per-write counter so the client's attribute
// cache invalidates on every WRITE.
func (f *mtpFile) fillStaged(sf *staging.File, requested virtual.AttributesMask, a *virtual.Attributes) {
	// Mandatory regardless of the requested mask (see fillCommon / M-006).
	a.SetFileHandle(encodeHandle(sf.Handle()))
	a.SetHasNamedAttributes(false)
	a.SetIsInNamedAttributeDirectory(false)

	if requested&virtual.AttributesMaskFileType != 0 {
		a.SetFileType(virtual.FileTypeRegularFile)
	}
	if requested&virtual.AttributesMaskPermissions != 0 {
		a.SetPermissions(virtual.PermissionsRead | virtual.PermissionsWrite)
	}
	if requested&virtual.AttributesMaskSizeBytes != 0 {
		if size, err := sf.Size(); err == nil {
			a.SetSizeBytes(size)
		}
	}
	if requested&virtual.AttributesMaskLinkCount != 0 {
		a.SetLinkCount(1)
	}
	if requested&virtual.AttributesMaskInodeNumber != 0 {
		a.SetInodeNumber(uint64(sf.Handle()))
	}
	if requested&virtual.AttributesMaskLastDataModificationTime != 0 {
		a.SetLastDataModificationTime(time.Now())
	}
	if requested&virtual.AttributesMaskChangeID != 0 {
		a.SetChangeID(sf.Change())
	}
	f.fillStatfs(requested, a)
}

// isAppleDouble reports whether an ObjectMap path is a macOS sidecar Finder
// writes alongside real files (`._name` AppleDouble resource forks, `.DS_Store`
// folder metadata). These are staged so OPEN/WRITE don't error, then dropped at
// flush rather than committed to the phone (see Root's idle callback).
func isAppleDouble(mtpPath string) bool {
	base := path.Base(mtpPath)
	return strings.HasPrefix(base, "._") || base == ".DS_Store"
}

// resolveStaged finalises one staged path: an AppleDouble sidecar is dropped
// (it's Finder bookkeeping, not a file the user dragged), anything else is
// flushed to the phone via SendFile. Shared by the idle-flush timer and
// VirtualClose so both honour the sidecar rule. The atomic delete inside
// Discard/Commit makes the two triggers race-safe — whichever fires first wins,
// the other is a no-op.
func resolveStaged(reg *staging.Registry, session *mtp.Session, mtpPath string) {
	if isAppleDouble(mtpPath) {
		reg.Discard(mtpPath)
		return
	}
	if err := reg.Commit(mtpPath, session, session.Objects); err != nil {
		log.Printf("staging flush %s: %v", mtpPath, err)
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
	if requested&virtual.AttributesMaskSizeBytes != 0 {
		a.SetSizeBytes(0)
	}
	if requested&virtual.AttributesMaskLinkCount != 0 {
		a.SetLinkCount(virtual.EmptyDirectoryLinkCount)
	}
	fillCommon(&meta, id, requested, a)
	d.fillStatfs(requested, a)
}

func (f *mtpFile) VirtualGetAttributes(ctx context.Context, requested virtual.AttributesMask, a *virtual.Attributes) {
	// Staging takes precedence: a file mid-upload has no device object yet, so
	// its size/handle/changeID come from the temp file, not the ObjectMap. This
	// must run before the GetByPath-miss early return, or a freshly created file
	// would report type-only and Finder's post-create stat would see size 0
	// forever.
	if sf := f.staged(); sf != nil {
		f.fillStaged(sf, requested, a)
		return
	}
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
	f.fillStatfs(requested, a)
}

// VirtualSetAttributes on a file. The macOS NFSv4 client issues SETATTR mid-copy
// — Finder preserves the source's timestamps and mode, and uses SETATTR(size=0)
// to truncate. We must answer it leniently or the copy aborts. Apply only what
// `in` actually carries (its fieldsPresent), NOT what `requested` asks to read
// back — keying off `requested` silently drops the truncate (the lesson baked
// into memory.go's own comment). After applying, fill `out` through
// VirtualGetAttributes so no mandatory attribute is left unset (the M-006 panic).
func (f *mtpFile) VirtualSetAttributes(ctx context.Context, in *virtual.Attributes, requested virtual.AttributesMask, out *virtual.Attributes) virtual.Status {
	if sf := f.staged(); sf != nil {
		if size, ok := in.GetSizeBytes(); ok {
			if err := sf.Truncate(int64(size)); err != nil {
				return virtual.StatusErrIO
			}
		}
		// Permissions / mtime have no home on MTP; accept them as a no-op so the
		// metadata-preserving SETATTR doesn't abort the copy.
		f.VirtualGetAttributes(ctx, requested, out)
		return virtual.StatusOK
	}
	// Committed device object: a size change (in-place truncate) needs
	// delete-then-restage and is a later increment, so reject it; metadata-only
	// SETATTRs (mtime/mode) are accepted as a no-op so timestamp-preserving
	// copies onto existing files don't error.
	if _, ok := in.GetSizeBytes(); ok {
		return virtual.StatusErrROFS
	}
	f.VirtualGetAttributes(ctx, requested, out)
	return virtual.StatusOK
}

// VirtualSetAttributes on a directory: MTP folders have no mutable attributes,
// but Finder sets a folder's mtime when copying into it — accept as a no-op and
// report current attributes.
func (d *mtpDir) VirtualSetAttributes(ctx context.Context, in *virtual.Attributes, requested virtual.AttributesMask, out *virtual.Attributes) virtual.Status {
	d.VirtualGetAttributes(ctx, requested, out)
	return virtual.StatusOK
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
	if d.mpath != "/" {
		d.session.EnsureInMap(d.mpath)
	}
	d.session.EnsurePopulated(d.mpath)
	childPath := path.Join(d.mpath, name.String())
	meta, ok := d.session.Objects.GetByPath(childPath)
	if !ok {
		return virtual.DirectoryChild{}, virtual.StatusErrNoEnt
	}
	child := childFor(d.session, d.reg, meta)
	if dir, leaf := child.GetPair(); dir != nil {
		dir.VirtualGetAttributes(ctx, requested, out)
	} else if leaf != nil {
		leaf.VirtualGetAttributes(ctx, requested, out)
	}
	return child, virtual.StatusOK
}

// VirtualReadDir enumerates children via ObjectMap.ListChildren after ensuring
// the directory is populated (a single libmtp OpListDir, serialised in
// session.Do). Cookies are 1-based indices into a name-sorted stable order, so
// an NFSv4 READDIR that resumes at firstCookie skips already-reported entries.
// Mirrors fs.go ReadDir (minus the synthetic sentinels — deferred).
func (d *mtpDir) VirtualReadDir(ctx context.Context, firstCookie uint64, requested virtual.AttributesMask, reporter virtual.DirectoryEntryReporter) virtual.Status {
	if d.mpath != "/" {
		d.session.EnsureInMap(d.mpath)
	}
	d.session.EnsurePopulated(d.mpath)
	children := d.session.Objects.ListChildren(d.mpath)
	sort.Slice(children, func(i, j int) bool { return children[i].Name < children[j].Name })
	for i, meta := range children {
		cookie := uint64(i + 1)
		if cookie <= firstCookie {
			continue
		}
		name, ok := virtual.NewComponent(meta.Name)
		if !ok {
			continue // skip names that aren't valid path components
		}
		child := childFor(d.session, d.reg, meta)
		var attributes virtual.Attributes
		if dir, leaf := child.GetPair(); dir != nil {
			dir.VirtualGetAttributes(ctx, requested, &attributes)
		} else if leaf != nil {
			leaf.VirtualGetAttributes(ctx, requested, &attributes)
		}
		if !reporter.ReportEntry(cookie, name, child, &attributes) {
			break
		}
	}
	return virtual.StatusOK
}

// ---- data ops (must marshal through session.Do — see package doc) ---------

// VirtualOpenSelf opens an existing leaf. MTP has no open primitive; for the
// read-only path we validate existence and accept read access (reject writes
// until the staged-write port lands). Truncate is a write and is refused.
func (f *mtpFile) VirtualOpenSelf(ctx context.Context, shareAccess virtual.ShareMask, options *virtual.OpenExistingOptions, requested virtual.AttributesMask, attributes *virtual.Attributes) virtual.Status {
	if shareAccess&virtual.ShareMaskWrite != 0 || (options != nil && options.Truncate) {
		return virtual.StatusErrROFS
	}
	meta, ok := f.session.Objects.GetByPath(f.mpath)
	if !ok {
		return virtual.StatusErrStale
	}
	f.VirtualGetAttributes(ctx, requested, attributes)
	_ = meta
	return virtual.StatusOK
}

// VirtualRead is the heart of the migration: a plain ranged read straight off
// the device via OpGetPartial(ObjectID, offset, len(buf)) — serialised through
// the session goroutine. No JUKEBOX, no threshold, no prefetch. NFSv4 tolerates
// the multi-minute read NFSv3's RPC-timeout window could not, so a slow/large
// read simply streams. (Assumes libmtp's partial read genuinely seeks rather
// than re-reading from byte 0 — verified empirically on first large read.)
func (f *mtpFile) VirtualRead(buf []byte, offset uint64) (int, bool, virtual.Status) {
	meta, ok := f.session.Objects.GetByPath(f.mpath)
	if !ok {
		return 0, false, virtual.StatusErrStale
	}
	bounded, eofBySize := virtual.BoundReadToFileSize(buf, offset, meta.Size)
	if len(bounded) == 0 {
		return 0, eofBySize, virtual.StatusOK
	}
	w := &sliceWriter{buf: bounded}
	resp := f.session.Do(mtp.MTPRequest{
		Op:       mtp.OpGetPartial,
		ObjectID: meta.ID,
		Offset:   offset,
		Size:     uint64(len(bounded)),
		Writer:   w,
	})
	if resp.Err != nil {
		return 0, false, virtual.StatusErrIO
	}
	n := w.n
	eof := eofBySize || n == 0 || offset+uint64(n) >= meta.Size
	return n, eof, virtual.StatusOK
}

// VirtualWrite buffers an NFSv4 WRITE into the staging temp file at the given
// offset (no device I/O — MTP has no partial write; the whole file is sent by
// SendFile when the idle timer fires, see staging.Commit). It only handles files
// opened through the create path, which seeded a staging entry; a WRITE to a
// committed device object has no entry and is refused (in-place overwrite of an
// existing phone file is a later increment). Each WRITE resets the idle timer.
func (f *mtpFile) VirtualWrite(buf []byte, offset uint64) (int, virtual.Status) {
	sf := f.staged()
	if sf == nil {
		return 0, virtual.StatusErrROFS
	}
	n, err := sf.WriteAt(buf, int64(offset))
	if err != nil {
		return n, virtual.StatusErrIO
	}
	return n, virtual.StatusOK
}

// VirtualClose is deliberately a no-op: the idle timer is the sole commit
// trigger. CLOSE cannot drive the commit because the macOS NFSv4 client copies a
// file as OPEN(create) → CLOSE(empty) → re-OPEN → WRITE → CLOSE — committing on
// that first (empty) close would SendFile a 0-byte file, delete the staging
// entry, and turn the re-OPEN-for-write into a write against a now-committed
// device object, which we reject (StatusErrROFS) → Finder reports a "locked
// volume". Letting the staging entry live across all the client's opens and
// flushing 2 s after the *last* WRITE is the willscott-proven behaviour (1 GB
// round-trip, R7). The trade-off — a >2 s mid-copy write stall commits a partial
// file — is the documented follow-up, addressed later by tracking open count
// rather than by an eager close-commit.
func (f *mtpFile) VirtualClose(shareAccess virtual.ShareMask) {}

// VirtualAllocate / VirtualSeek — MTP has neither fallocate nor sparse seeking.
func (f *mtpFile) VirtualAllocate(off, size uint64) virtual.Status { return virtual.StatusErrInval }
func (f *mtpFile) VirtualSeek(offset uint64, regionType virtual.RegionType) (*uint64, virtual.Status) {
	return nil, virtual.StatusErrInval
}

// ---- mutation ops — read-only for now (writes are the next increment) ------
//
// Each returns StatusErrROFS until the staged-write path is ported. When it
// lands: VirtualOpenChild(create)+VirtualWrite via the extracted writeRegistry,
// VirtualMkdir via OpCreateFolder, VirtualRemove via OpDelete, VirtualRename via
// copy+delete (MTP has no native rename) — all through session.Do.

// VirtualOpenChild is the NFSv4 OPEN entry point (OPEN is parent-dir + name, so
// even a plain read open lands here, not VirtualOpenSelf). Opening an existing
// file for read is supported; create (createAttributes != nil), write
// (ShareMaskWrite), and truncate are ROFS until the write port lands. Mirrors
// osfs.VirtualOpenChild.
func (d *mtpDir) VirtualOpenChild(ctx context.Context, name virtual.Component, shareAccess virtual.ShareMask, createAttributes *virtual.Attributes, existingOptions *virtual.OpenExistingOptions, requested virtual.AttributesMask, openedFileAttributes *virtual.Attributes) (virtual.Leaf, virtual.AttributesMask, virtual.ChangeInfo, virtual.Status) {
	ci := virtual.ChangeInfo{}
	if d.mpath != "/" {
		d.session.EnsureInMap(d.mpath)
	}
	d.session.EnsurePopulated(d.mpath)
	childPath := path.Join(d.mpath, name.String())

	// Already staging (created earlier this session, not yet committed): the
	// device map has no object for it, so this must be checked before GetByPath.
	// Hand back the same staged leaf; an O_TRUNC re-open resets the buffer.
	if sf := d.reg.Get(childPath); sf != nil {
		if existingOptions != nil && existingOptions.Truncate {
			if err := sf.Truncate(0); err != nil {
				return nil, 0, ci, virtual.StatusErrIO
			}
		}
		leaf := &mtpFile{node{session: d.session, reg: d.reg, mpath: childPath}}
		leaf.VirtualGetAttributes(ctx, requested, openedFileAttributes)
		return leaf, 0, ci, virtual.StatusOK
	}

	meta, ok := d.session.Objects.GetByPath(childPath)
	if !ok {
		if createAttributes != nil {
			// New file: seed a staging entry (synthetic handle + temp file).
			// WRITEs buffer there; the idle timer commits it to the phone via
			// SendFile (or discards it, for an AppleDouble sidecar).
			if _, err := d.reg.Register(childPath); err != nil {
				return nil, 0, ci, virtual.StatusErrIO
			}
			leaf := &mtpFile{node{session: d.session, reg: d.reg, mpath: childPath}}
			leaf.VirtualGetAttributes(ctx, requested, openedFileAttributes)
			return leaf, 0, ci, virtual.StatusOK
		}
		return nil, 0, ci, virtual.StatusErrNoEnt
	}
	if existingOptions == nil {
		return nil, 0, ci, virtual.StatusErrExist // exclusive-create on an existing object
	}
	if meta.IsDir {
		return nil, 0, ci, virtual.StatusErrIsDir
	}
	if shareAccess&virtual.ShareMaskWrite != 0 || existingOptions.Truncate {
		// In-place overwrite/truncate of a committed device object is a later
		// increment (it needs delete-then-restage); reads are fine.
		return nil, 0, ci, virtual.StatusErrROFS
	}
	leaf := &mtpFile{node{session: d.session, reg: d.reg, mpath: childPath}}
	leaf.VirtualGetAttributes(ctx, requested, openedFileAttributes)
	return leaf, 0, ci, virtual.StatusOK
}

func (d *mtpDir) VirtualMkdir(name virtual.Component, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Directory, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *mtpDir) VirtualMknod(ctx context.Context, name virtual.Component, fileType virtual.FileType, requested virtual.AttributesMask, attributes *virtual.Attributes) (virtual.Leaf, virtual.ChangeInfo, virtual.Status) {
	return nil, virtual.ChangeInfo{}, virtual.StatusErrInval
}

func (d *mtpDir) VirtualRemove(name virtual.Component, removeDirectory, removeLeaf bool) (virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.StatusErrROFS
}

func (d *mtpDir) VirtualRename(oldName virtual.Component, newDirectory virtual.Directory, newName virtual.Component) (virtual.ChangeInfo, virtual.ChangeInfo, virtual.Status) {
	return virtual.ChangeInfo{}, virtual.ChangeInfo{}, virtual.StatusErrROFS
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
