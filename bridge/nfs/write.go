package nfs

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	billy "github.com/go-git/go-billy/v5"

	"comprador/bridge/mtp"
)

// writeRegistry tracks in-progress MTP uploads as staging temp files.
// When go-nfs issues CREATE it adds an entry; on COMMIT (or fileSync WRITE,
// or idle-timeout) the entry is flushed to MTP and removed. Concurrent
// WRITE RPCs share the same temp file via stagingHandle.WriteAt, which is
// concurrency-safe on *os.File.
//
// macOS NFSv3 clients do not reliably send COMMIT RPCs (verified empirically
// 2026-05-08: writes sit dirty across sync, fsync, and graceful unmount).
// Two preemptive flush paths exist:
//
//   1. Idle-flush: a per-entry timer reset on every WRITE; commits to MTP
//      after idleFlushInterval of inactivity. This is the catch-all for
//      clients that issue no fileSync WRITE and no COMMIT (older or
//      misbehaving clients).
//
//   2. Synchronous flush on fileSync WRITE: macOS's `cp` ends a copy with
//      a single WRITE having stability=fileSync. The vendored go-nfs
//      onWrite handler type-asserts the file to DurableSyncer and calls
//      SyncDurable, which routes to stagingFile.syncCommit and BLOCKS
//      until the MTP push completes. This is the mechanism that keeps
//      Finder's progress dialog up for the real duration of a large
//      transfer — see the "Design choice: single source of truth in
//      Finder's progress dialog" block comment below.
//
// Both paths converge in commitOnce, which uses sync.Once to ensure that
// regardless of how many fire (idle timer + fileSync simultaneously, or
// duplicate fileSync retransmits during a slow MTP send), the actual MTP
// upload runs exactly once and all callers see the same result via the
// done channel.
type writeRegistry struct {
	mu      sync.Mutex
	pending map[string]*stagingFile // keyed by MTP path ("/storage/dir/file")
	session *mtp.Session
	objects *mtp.ObjectMap
	idle    time.Duration
}

// idleFlushInterval is how long the registry waits after the last WRITE
// before assuming the writer is done and flushing to MTP. Tuned to be
// long enough that a multi-WRITE upload doesn't get split mid-stream
// (macOS sends WRITEs roughly every few ms during a copy) but short
// enough that the user perceives the file as "saved" promptly.
const idleFlushInterval = 2 * time.Second

// =============================================================================
// Design choice: single source of truth in Finder's progress dialog
// =============================================================================
//
// Finder's copy-progress sheet is the only progress UI the user sees during
// a drag-drop. The brand promise of Comprador is *don't add another UI;
// don't make the user manage two states of "done"*. So Finder's progress
// bar must be the truth.
//
// What "truth" means here: when Finder's progress dialog dismisses, the
// bytes are durable on the phone. Not "in the bridge's staging temp file."
// Not "queued for MTP send." Durable on the phone, byte-perfect.
//
// The historic NFS server pattern for slow backing stores is to ACK every
// WRITE at memory speed and rely on a follow-up COMMIT RPC for durability.
// macOS NFSv3 clients in practice do not reliably send COMMIT (verified
// 2026-05-08); they signal "this is the end" by setting stability=fileSync
// on the last WRITE of a file. Comprador's earlier behaviour took the
// memory-speed ACK route and used an idle-flush timer to push the
// resulting staged file to MTP asynchronously. The consequence: Finder's
// progress bar reached 100% in ~30 seconds for a 9 GB file (the time to
// receive 9 GB of WRITE RPCs over loopback NFS), the dialog dismissed,
// and the user believed the copy was done — while the bridge was still
// 7 minutes away from finishing the actual MTP send over USB at ~22 MB/s.
// This was reported as a "silent regression" on 2026-05-14; it was in
// fact a long-standing progress-bar-lies bug surfacing as a confused user.
//
// We took the most honest available correction here: hold the fileSync
// WRITE's RPC response until the MTP send has actually completed. The
// effect on Finder: the progress bar fills fast to ~100% (as bytes hit
// the bridge's RAM/staging), then the dialog *stays up* with the bar
// hovering at near-100% for the duration of the MTP push, dismissing
// only when the phone has all the bytes. The user's sense of "Finder
// says it's done" now matches "the phone has the file."
//
// Why not accurately reflect MTP progress in the bar itself (instead of
// the trailing hover)? Three structural constraints made it infeasible:
//
//   - MTP requires the object size up front when initiating SendObject.
//     The phone allocates and the receiver expects exactly that many
//     bytes. We cannot start the MTP send until we know the size, and
//     the size only becomes knowable when the writer signals done.
//   - NFSv3 WRITE RPCs do not carry a size hint. Finder issues many
//     32 KB WRITEs at arbitrary offsets; we discover the high-water
//     mark only after writes stop. We surveyed the WRITE RPC stream
//     from a 9 GB Finder drag (2026-05-14, build/dev-nfs.log) — no
//     SETATTR with size precedes the WRITEs, and there's no other
//     mechanism in the protocol for the client to declare intent.
//   - The two facts above mean MTP cannot run concurrently with NFS
//     writes; the staging phase must complete first. So even if we
//     held back per-WRITE ACKs to throttle the apparent rate to MTP
//     speed, the bar would still fill fast (the kernel's write buffer
//     can absorb minutes of unacked traffic at memory speed) and then
//     hover. Same UX, more complexity.
//
// The hovering-bar pattern is a known macOS Finder idiom — system-builtin
// NFS shares, AFP volumes to slow backends, and large App Store updates
// all exhibit it for the same protocol reason. Users tolerate it; what
// they don't tolerate is a dismissed dialog hiding a still-running copy.
//
// If a future macOS NFS client (or revised Finder copy logic) ever
// announces target size before WRITEs — e.g. via SETATTR.size before
// the first WRITE — we can revisit. The pieces would be: capture size
// from SETATTR, start MTP SendObject early with the announced size,
// feed WRITE bytes through a streaming reader into the MTP send
// callback, and ACK each WRITE only as its bytes are consumed by MTP.
// Per-chunk ACK throttling would then translate directly to bar
// movement at USB-MTP rate. The plumbing is non-trivial but not
// architecturally novel; it's parked until the size-hint signal
// appears in real client traffic.
//
// =============================================================================

func newWriteRegistry(session *mtp.Session, objects *mtp.ObjectMap) *writeRegistry {
	return &writeRegistry{
		pending: make(map[string]*stagingFile),
		session: session,
		objects: objects,
		idle:    idleFlushInterval,
	}
}

func (r *writeRegistry) get(mtpPath string) *stagingFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[mtpPath]
}

// register creates a new staging temp file for mtpPath. billyPath is the
// original path as presented to fs.Create (no leading slash), kept for
// billy.File.Name() so that go-nfs tryStat calls resolve correctly.
func (r *writeRegistry) register(mtpPath, billyPath string) (*stagingFile, error) {
	tmp, err := os.CreateTemp("", "comprador-write-*")
	if err != nil {
		return nil, err
	}
	sf := &stagingFile{
		tmp:       tmp,
		billyPath: billyPath,
		mtpPath:   mtpPath,
		registry:  r,
		done:      make(chan struct{}),
	}
	// Timer is created stopped. Each Write resets it; idle expiry triggers
	// the commit path. The commit is idempotent (sync.Once) so a racing
	// fileSync syncCommit from the WRITE handler is safe.
	sf.timer = time.AfterFunc(r.idle, func() {
		sf.commitOnce(false /* synchronous */)
	})
	sf.timer.Stop()
	r.mu.Lock()
	r.pending[mtpPath] = sf
	r.mu.Unlock()
	return sf, nil
}

// touch resets the idle-flush timer for sf. Called by stagingHandle.Write
// after every successful write. Safe to call concurrently from multiple
// WRITE RPC goroutines.
func (sf *stagingFile) touch(idle time.Duration) {
	if sf.timer != nil {
		sf.timer.Reset(idle)
	}
}

// bumpDirMtime sets a directory's ModTime to now. NFSv3 clients
// invalidate their cached READDIR results when a GETATTR on the parent
// directory shows a newer mtime than the cached value, so calling this
// after any directory-mutating op (commit, delete, rename) makes Finder
// re-enumerate within seconds rather than waiting on the client's
// natural attribute-cache TTL. No-op if dir is nil.
func bumpDirMtime(dir *mtp.ObjectMeta, objects *mtp.ObjectMap) {
	if dir == nil {
		return
	}
	dir.ModTime = time.Now()
	objects.Put(dir)
}

// rekey moves a staging entry from oldPath to newPath. Used by fs.Rename
// when Finder renames a freshly-written ".tmpXXXX" temp to its final name
// before the idle-flush timer fires for the temp. Returns true if there
// was an entry at oldPath; the caller should treat false as "source isn't
// in staging — fall back to MTP-side copy+delete." The timer is rebuilt
// to flush under newPath; it starts armed so a Finder rename followed by
// no further writes still commits in idleFlushInterval.
func (r *writeRegistry) rekey(oldPath, newPath, newBillyPath string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	sf, ok := r.pending[oldPath]
	if !ok {
		return false
	}
	delete(r.pending, oldPath)
	if sf.timer != nil {
		sf.timer.Stop()
	}
	sf.billyPath = newBillyPath
	sf.mtpPath = newPath
	sf.timer = time.AfterFunc(r.idle, func() {
		sf.commitOnce(false /* synchronous */)
	})
	r.pending[newPath] = sf
	return true
}

// delete removes a staging entry and returns it (nil if not found).
func (r *writeRegistry) delete(mtpPath string) *stagingFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	sf := r.pending[mtpPath]
	delete(r.pending, mtpPath)
	return sf
}

type stagingFile struct {
	tmp       *os.File
	billyPath string      // as passed to Create, e.g. "Internal storage/DCIM/photo.jpg"
	mtpPath   string      // canonical "/storage/dir/file" path used as registry key
	registry  *writeRegistry
	timer     *time.Timer // idle-flush timer; reset on every Write

	// commit-once machinery: the actual MTP push runs at most once across
	// any combination of idle-timer firing, fileSync SyncDurable call,
	// retransmitted fileSync from the client, and explicit COMMIT RPC.
	once      sync.Once
	done      chan struct{} // closed when commit completes (success or fail)
	commitErr error
}

// stat returns an os.FileInfo for the staging file with Name() set to the
// base filename (billy convention: Name returns the base, not full path).
func (sf *stagingFile) stat() (os.FileInfo, error) {
	fi, err := sf.tmp.Stat()
	if err != nil {
		return nil, err
	}
	return &stagingFileInfo{FileInfo: fi, name: filepath.Base(sf.billyPath)}, nil
}

type stagingFileInfo struct {
	os.FileInfo
	name string
}

func (s *stagingFileInfo) Name() string { return s.name }

// stagingHandle is a billy.File backed by a staging temp file.
// Write uses WriteAt internally (concurrency-safe for concurrent WRITE RPCs
// at different offsets). Close is a no-op; the temp file is owned by the
// writeRegistry until COMMIT.
type stagingHandle struct {
	name string // billy path (no leading slash), returned by Name()
	sf   *stagingFile
	pos  int64
}

func (h *stagingHandle) Name() string { return h.name }

func (h *stagingHandle) Write(p []byte) (int, error) {
	n, err := h.sf.tmp.WriteAt(p, h.pos)
	h.pos += int64(n)
	if n > 0 {
		h.sf.touch(idleFlushInterval)
	}
	return n, err
}

func (h *stagingHandle) Read(p []byte) (int, error) {
	n, err := h.sf.tmp.ReadAt(p, h.pos)
	h.pos += int64(n)
	return n, err
}

func (h *stagingHandle) ReadAt(p []byte, off int64) (int, error) {
	return h.sf.tmp.ReadAt(p, off)
}

func (h *stagingHandle) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = h.pos + offset
	case io.SeekEnd:
		fi, err := h.sf.tmp.Stat()
		if err != nil {
			return 0, err
		}
		abs = fi.Size() + offset
	}
	if abs < 0 {
		abs = 0
	}
	h.pos = abs
	return abs, nil
}

func (h *stagingHandle) Truncate(size int64) error {
	return h.sf.tmp.Truncate(size)
}

func (h *stagingHandle) Lock() error   { return nil }
func (h *stagingHandle) Unlock() error { return nil }
func (h *stagingHandle) Close() error  { return nil } // owned by registry, not caller

// SyncDurable blocks the caller until the staged bytes for this file have
// been pushed to the MTP device. Called by the patched go-nfs onWrite
// handler when the client sends a WRITE with stability=fileSync — see
// COMPRADOR-PATCH in vendor/.../nfs_onwrite.go. The trade-off is
// documented in the "single source of truth in Finder's progress dialog"
// block comment at the top of this file.
//
// Idempotent across multiple calls (commitOnce backs it with sync.Once);
// a duplicate fileSync retransmit from a macOS client whose RPC timeout
// fires during a multi-minute MTP send will reach the same in-flight
// commit and block on the same done channel, not start a second send.
func (h *stagingHandle) SyncDurable() error {
	return h.sf.commitOnce(true /* wait */)
}

// discardingHandle is the billy.File returned by Create for AppleDouble
// (`._*`) paths. Writes are silently dropped; reads return EOF. The point
// is that macOS Finder sees NFSStatusOk for every WRITE+COMMIT on the
// AppleDouble companion file it tries to make, so the drag-drop doesn't
// surface an error — but no bytes reach the phone. See
// isAppleDoubleBasename in fs.go for the rationale.
//
// Lives in this file rather than fs.go to sit alongside stagingHandle:
// they're sibling billy.File implementations and reading them together
// makes the contrast obvious.
type discardingHandle struct {
	name string
	pos  int64
}

func (h *discardingHandle) Name() string                          { return h.name }
func (h *discardingHandle) Write(p []byte) (int, error)           { h.pos += int64(len(p)); return len(p), nil }
func (h *discardingHandle) Read(_ []byte) (int, error)            { return 0, io.EOF }
func (h *discardingHandle) ReadAt(_ []byte, _ int64) (int, error) { return 0, io.EOF }
func (h *discardingHandle) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		h.pos = offset
	case io.SeekCurrent:
		h.pos += offset
	case io.SeekEnd:
		h.pos = offset
	}
	if h.pos < 0 {
		h.pos = 0
	}
	return h.pos, nil
}
func (h *discardingHandle) Truncate(_ int64) error { return nil }
func (h *discardingHandle) Lock() error            { return nil }
func (h *discardingHandle) Unlock() error          { return nil }
func (h *discardingHandle) Close() error           { return nil }

// SyncDurable on a discardingHandle is a no-op — there are no staged
// bytes to push because writes were dropped on the floor. Implementing
// the interface keeps the fileSync path uniform; the WRITE handler can
// call SyncDurable on any file without branching on AppleDouble status.
func (h *discardingHandle) SyncDurable() error { return nil }

// commitOnce is the single entry point for flushing a staging entry to
// MTP. It is safe to call from any of: the idle-flush timer, the
// SyncDurable hook fired by a fileSync WRITE, the explicit Commit RPC
// handler, or a duplicate fileSync retransmit during a multi-minute MTP
// push. sync.Once ensures the MTP send runs exactly once; subsequent
// callers block on the done channel and receive the same commitErr.
//
// wait controls whether the caller blocks on completion. The idle timer
// uses wait=false (fire-and-forget; the goroutine running the MTP push
// is the one that logs success/failure). The SyncDurable path uses
// wait=true so the WRITE RPC response is held until the bytes are
// durable on the phone — see the "single source of truth" block comment
// at the top of this file.
func (sf *stagingFile) commitOnce(wait bool) error {
	sf.once.Do(func() {
		go func() {
			err := sf.registry.doCommit(sf)
			sf.commitErr = err
			close(sf.done)
			if err != nil {
				log.Printf("idle-flush %s: %v", sf.mtpPath, err)
			} else {
				log.Printf("idle-flush %s: committed", sf.mtpPath)
			}
		}()
	})
	if !wait {
		return nil
	}
	<-sf.done
	return sf.commitErr
}

// doCommit is the actual MTP-send work, called from inside the once goroutine.
// Not safe to call directly — go through commitOnce.
func (r *writeRegistry) doCommit(sf *stagingFile) error {
	if sf.timer != nil {
		sf.timer.Stop()
	}
	// Remove from the pending map. We do this *before* the MTP send (which
	// can take minutes) so that any new OpenFile for this path goes to the
	// MTP lookup branch rather than reusing this staging entry mid-send.
	// Duplicate fileSync retransmits during the long MTP push hit
	// commitOnce, find the work in flight via sync.Once, and block on
	// sf.done — they don't try to access r.pending.
	r.mu.Lock()
	delete(r.pending, sf.mtpPath)
	r.mu.Unlock()

	mtpPath := sf.mtpPath
	session := r.session
	objects := r.objects

	fi, err := sf.tmp.Stat()
	if err != nil {
		return fmt.Errorf("staging stat: %w", err)
	}
	size := uint64(fi.Size())

	// Resolve parent.
	parentMTPPath := cleanPath(filepath.Dir(strings.TrimPrefix(mtpPath, "/")))
	parentMeta, ok := objects.GetByPath(parentMTPPath)
	if !ok {
		return fmt.Errorf("parent not in object map: %s", parentMTPPath)
	}

	// If the file already exists in MTP (overwrite), delete the old object first.
	if existing, ok := objects.GetByPath(mtpPath); ok {
		resp := session.Do(mtp.MTPRequest{Op: mtp.OpDelete, ObjectID: existing.ID})
		if resp.Err != nil {
			return fmt.Errorf("delete existing: %w", resp.Err)
		}
		objects.Remove(mtpPath)
	}

	// Rewind and upload.
	if _, err := sf.tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("staging seek: %w", err)
	}
	fileName := filepath.Base(mtpPath)
	resp := session.Do(mtp.MTPRequest{
		Op:        mtp.OpSendFile,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      fileName,
		Size:      size,
		Reader:    sf.tmp,
	})
	if resp.Err != nil {
		return fmt.Errorf("MTP upload: %w", resp.Err)
	}

	// Register new object in the map.
	objects.Put(&mtp.ObjectMeta{
		ID:        resp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      fileName,
		Path:      mtpPath,
		Size:      size,
		ModTime:   time.Now(),
		IsDir:     false,
	})

	// Bump the parent's mtime so Finder/the NFS client invalidates
	// any cached READDIR for this directory and surfaces the new file
	// without waiting on the attribute-cache TTL.
	bumpDirMtime(parentMeta, objects)

	// Discard staging file.
	sf.tmp.Close()
	os.Remove(sf.tmp.Name())
	return nil
}

// noopChange implements billy.Change with no-ops so that go-nfs's
// SetFileAttributes.Apply does not error when macOS sets chmod/mtime on
// a newly created file. MTP has no notion of Unix permissions.
type noopChange struct{}

func (noopChange) Chmod(_ string, _ os.FileMode) error           { return nil }
func (noopChange) Lchown(_ string, _, _ int) error               { return nil }
func (noopChange) Chown(_ string, _, _ int) error                { return nil }
func (noopChange) Chtimes(_ string, _, _ time.Time) error        { return nil }

// ensure noopChange satisfies the interface
var _ billy.Change = noopChange{}
