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
type writeRegistry struct {
	mu      sync.Mutex
	pending map[string]*stagingFile // keyed by MTP path ("/storage/dir/file")
}

func newWriteRegistry() *writeRegistry {
	return &writeRegistry{pending: make(map[string]*stagingFile)}
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
	r.mu.Lock()
	r.pending[mtpPath] = sf
	r.mu.Unlock()
	return sf, nil
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
	billyPath string // as passed to Create, e.g. "Internal storage/DCIM/photo.jpg"
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
// Called by mtpNFSHandler.Commit when the NFS client issues a COMMIT RPC.
func (r *writeRegistry) commit(mtpPath string, session *mtp.Session, objects *mtp.ObjectMap) error {
	sf := r.get(mtpPath)
	if sf == nil {
		return nil // no pending write; COMMIT with no prior write is valid
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

	// Discard staging file.
	r.delete(mtpPath)
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
