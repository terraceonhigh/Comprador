// Package staging holds in-progress MTP uploads as local temp files and flushes
// them to the device. It is the write-side counterpart to the read path: MTP has
// no partial write, so a file being written is buffered to a temp file and sent
// whole (LIBMTP SendFile) once the writer goes idle or explicitly closes.
//
// It depends only on the mtp package and the stdlib — never on any NFS server
// (willscott/go-nfs or Galatea). That keeps it shareable: the FSAL
// (bridge/mtpfsal) drives it, and nothing here couples back to a transport. All
// device I/O goes through (*mtp.Session).Do, the single-goroutine serialization
// boundary; the temp-file writes do not (they're plain *os.File).
//
// # Synthetic handles
//
// NFSv4 needs a stable file handle at OPEN(create) time, but MTP assigns an
// object ID only at commit (SendFile). So each staged file gets a synthetic
// handle ID from a high-range counter, recorded here in byHandle; the FSAL's
// handle resolver consults the registry before the device's ObjectMap. The
// synthetic handle only has to resolve in the pre-commit window (Finder does
// OPEN→WRITE→CLOSE in ms; the idle commit fires seconds later, after which the
// client has discarded the handle and a re-browse yields the real object ID).
//
// Lifted from the proven logic in bridge/nfs/write.go (the willscott path),
// stripped of its billy.File wrappers and re-keyed on ObjectMap paths
// ("/storage/dir/file") since that's the form the FSAL speaks.
package staging

import (
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"comprador/bridge/mtp"
)

// DefaultIdleFlush is how long the registry waits after the last write before
// assuming the writer is done and flushing to MTP. Long enough that a multi-write
// upload isn't split mid-stream, short enough that the file appears "saved"
// promptly. NFS clients do not reliably send a close/COMMIT we can trust
// (verified on the willscott path 2026-05-08), so the idle timer is the real
// commit trigger; an explicit Commit() preempts it.
const DefaultIdleFlush = 2 * time.Second

// firstSyntheticHandle starts the synthetic-handle counter well above any
// plausible MTP object ID (Android assigns small uint32s), so a synthetic handle
// is vanishingly unlikely to collide with a real one — and the resolver checks
// the registry first regardless, so a staged file always wins in its window.
const firstSyntheticHandle uint32 = 1 << 31

// Registry tracks staging files keyed by their MTP (ObjectMap) path.
type Registry struct {
	mu       sync.Mutex
	pending  map[string]*File   // mtpPath -> file
	byHandle map[uint32]string  // synthetic handle -> mtpPath
	nextID   uint32
	flush    func(mtpPath string) // commits one entry; must not block the caller
	idle     time.Duration
}

// NewRegistry builds a registry. flush is invoked from a file's idle timer when
// it expires; it should commit that single path (typically a closure calling
// (*Registry).Commit with the session). flush must not block.
func NewRegistry(flush func(mtpPath string)) *Registry {
	return &Registry{
		pending:  make(map[string]*File),
		byHandle: make(map[uint32]string),
		nextID:   firstSyntheticHandle,
		flush:    flush,
		idle:     DefaultIdleFlush,
	}
}

// File is a single staging upload: a temp file plus the name it will carry on
// the device, a synthetic handle, a change counter (for ChangeID), and an
// idle-flush timer reset on every write.
type File struct {
	tmp      *os.File
	name     string // base filename for the MTP SendFile
	handle   uint32 // synthetic NFS handle, stable for this file's staged life
	change   uint64 // bumped on every write/truncate; surfaces as ChangeID
	timer    *time.Timer
	reg      *Registry
}

// Register creates a new staging temp file for mtpPath with a fresh synthetic
// handle. Replaces any existing entry at that path (a re-create/truncate).
func (r *Registry) Register(mtpPath string) (*File, error) {
	tmp, err := os.CreateTemp("", "comprador-write-*")
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	handle := r.nextID
	r.nextID++
	f := &File{tmp: tmp, name: path.Base(mtpPath), handle: handle, reg: r}
	f.timer = time.AfterFunc(r.idle, func() { r.flush(mtpPath) })
	f.timer.Stop()
	r.pending[mtpPath] = f
	r.byHandle[handle] = mtpPath
	r.mu.Unlock()
	return f, nil
}

// Get returns the staging file for mtpPath, or nil if none is in progress.
func (r *Registry) Get(mtpPath string) *File {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[mtpPath]
}

// PathForHandle resolves a synthetic handle to its staged path. The FSAL's
// handle resolver calls this before falling back to the device ObjectMap.
func (r *Registry) PathForHandle(handle uint32) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byHandle[handle]
	return p, ok
}

// delete removes and returns the entry (nil if absent), clearing its handle
// mapping. Atomic, so the explicit Commit and the idle-timer flush race safely —
// only one sees a non-nil File.
func (r *Registry) delete(mtpPath string) *File {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.pending[mtpPath]
	if f != nil {
		delete(r.byHandle, f.handle)
	}
	delete(r.pending, mtpPath)
	return f
}

// Discard drops a staging entry without uploading (e.g. the file was removed
// before it flushed). Closes and unlinks the temp file.
func (r *Registry) Discard(mtpPath string) {
	if f := r.delete(mtpPath); f != nil {
		if f.timer != nil {
			f.timer.Stop()
		}
		f.tmp.Close()
		os.Remove(f.tmp.Name())
	}
}

// Rekey moves a staging entry from oldPath to newPath (a rename of a file still
// in staging, before it flushed). Returns false if oldPath has no entry — the
// caller should then treat the rename as a device-side copy+delete. The handle
// is preserved; the timer is rebuilt to flush under newPath, armed.
func (r *Registry) Rekey(oldPath, newPath string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.pending[oldPath]
	if !ok {
		return false
	}
	delete(r.pending, oldPath)
	if f.timer != nil {
		f.timer.Stop()
	}
	f.name = path.Base(newPath)
	f.timer = time.AfterFunc(r.idle, func() { r.flush(newPath) })
	r.pending[newPath] = f
	r.byHandle[f.handle] = newPath
	return true
}

// Handle returns the file's synthetic NFS handle.
func (f *File) Handle() uint32 { return f.handle }

// Change returns a monotonically increasing counter that advances on every
// write — used as the NFSv4 ChangeID so the client's attribute cache invalidates
// as the staged file grows.
func (f *File) Change() uint64 { return atomic.LoadUint64(&f.change) }

// WriteAt writes into the staging temp at offset (concurrency-safe across NFSv4
// WRITE goroutines — *os.File.WriteAt is) and resets the idle-flush timer.
func (f *File) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.tmp.WriteAt(p, off)
	if n > 0 {
		atomic.AddUint64(&f.change, 1)
		if f.timer != nil {
			f.timer.Reset(f.reg.idle)
		}
	}
	return n, err
}

// Truncate resizes the staging temp (O_TRUNC on open, or a SETATTR size change).
func (f *File) Truncate(size int64) error {
	atomic.AddUint64(&f.change, 1)
	if f.timer != nil {
		f.timer.Reset(f.reg.idle)
	}
	return f.tmp.Truncate(size)
}

// Size returns the current staged byte count.
func (f *File) Size() (uint64, error) {
	fi, err := f.tmp.Stat()
	if err != nil {
		return 0, err
	}
	return uint64(fi.Size()), nil
}

// Commit flushes the staging file for mtpPath to the device and updates the
// object map, then discards the temp file. Safe to call concurrently with the
// idle timer (the atomic delete picks a single winner). A no-op if nothing is
// staged at mtpPath. All device ops go through session.Do.
func (r *Registry) Commit(mtpPath string, session *mtp.Session, objects *mtp.ObjectMap) error {
	f := r.delete(mtpPath)
	if f == nil {
		return nil
	}
	if f.timer != nil {
		f.timer.Stop()
	}

	size, err := f.Size()
	if err != nil {
		return fmt.Errorf("staging stat: %w", err)
	}

	parentPath := parentOf(mtpPath)
	parentMeta, ok := objects.GetByPath(parentPath)
	if !ok {
		return fmt.Errorf("parent not in object map: %s", parentPath)
	}

	// Overwrite: delete the existing device object first (MTP SendFile to an
	// existing name would create a duplicate, not replace).
	if existing, ok := objects.GetByPath(mtpPath); ok {
		resp := session.Do(mtp.MTPRequest{Op: mtp.OpDelete, ObjectID: existing.ID})
		if resp.Err != nil {
			return fmt.Errorf("delete existing: %w", resp.Err)
		}
		objects.Remove(mtpPath)
	}

	if _, err := f.tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("staging seek: %w", err)
	}
	resp := session.Do(mtp.MTPRequest{
		Op:        mtp.OpSendFile,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      f.name,
		Size:      size,
		Reader:    f.tmp,
	})
	if resp.Err != nil {
		return fmt.Errorf("MTP upload: %w", resp.Err)
	}

	objects.Put(&mtp.ObjectMeta{
		ID:        resp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      f.name,
		Path:      mtpPath,
		Size:      size,
		ModTime:   time.Now(),
		IsDir:     false,
	})

	// Bump the parent's mtime so the NFS client invalidates its cached READDIR
	// and Finder surfaces the new file without waiting on the attribute-cache
	// TTL (the willscott commit did the same).
	parentMeta.ModTime = time.Now()
	objects.Put(parentMeta)

	f.tmp.Close()
	os.Remove(f.tmp.Name())
	return nil
}

// parentOf returns the ObjectMap parent path of an ObjectMap path. The parent of
// a top-level entry, or of root, is "/".
func parentOf(p string) string {
	d := path.Dir(p)
	if d == "." || d == "" {
		return "/"
	}
	return d
}
