package nfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	billy "github.com/go-git/go-billy/v5"

	"comprador/bridge/mtp"
)

// writeRegistry tracks in-progress MTP uploads as staging temp files.
// When go-nfs issues CREATE it adds an entry; on COMMIT the entry is flushed
// to MTP and removed. Concurrent WRITE RPCs share the same temp file via
// stagingHandle.WriteAt, which is concurrency-safe on *os.File.
//
// macOS NFSv3 clients do not reliably send COMMIT RPCs (verified empirically
// 2026-05-08: writes sit dirty across sync, fsync, and graceful unmount).
// We therefore arm a per-entry idle timer on every WRITE; if no further
// writes arrive within idleFlushInterval, the registry flushes to MTP on
// its own. An explicit client COMMIT preempts the timer.
type writeRegistry struct {
	mu      sync.Mutex
	pending map[string]*stagingFile // keyed by MTP path ("/storage/dir/file")
	flush   func(mtpPath string)    // commits a single staging entry; never blocks the caller
	idle    time.Duration
}

// idleFlushInterval is how long the registry waits after the last WRITE
// before assuming the writer is done and flushing to MTP. Tuned to be
// long enough that a multi-WRITE upload doesn't get split mid-stream
// (macOS sends WRITEs roughly every few ms during a copy) but short
// enough that the user perceives the file as "saved" promptly.
const idleFlushInterval = 2 * time.Second

func newWriteRegistry(flush func(mtpPath string)) *writeRegistry {
	return &writeRegistry{
		pending: make(map[string]*stagingFile),
		flush:   flush,
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
	sf := &stagingFile{tmp: tmp, billyPath: billyPath}
	// Timer is created stopped. Each Write resets it; idle expiry triggers flush.
	sf.timer = time.AfterFunc(r.idle, func() { r.flush(mtpPath) })
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
	sf.timer = time.AfterFunc(r.idle, func() { r.flush(newPath) })
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
	timer     *time.Timer // idle-flush timer; reset on every Write
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

// commit flushes the staging file for mtpPath to the MTP device.
// Called from two paths: mtpNFSHandler.Commit (explicit client COMMIT RPC)
// and the per-entry idle timer set up in register/touch. The atomic delete
// at the top makes both callers race-safe — only one will see a non-nil sf.
func (r *writeRegistry) commit(mtpPath string, session *mtp.Session, objects *mtp.ObjectMap) error {
	sf := r.delete(mtpPath)
	if sf == nil {
		return nil // already committed, or COMMIT with no prior write
	}
	if sf.timer != nil {
		sf.timer.Stop()
	}

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
