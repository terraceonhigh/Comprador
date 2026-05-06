// Package resume manages the persisted partial-upload state used to
// recover from Apple WebDAVFS chunked-upload truncation.
//
// When webdavfs's writeseq path caps a chunked PUT at less than its
// X-Expected-Entity-Length, the WebDAV handler persists what it got
// (path, expected size, partial body) into this store and returns 200 OK
// to webdavfs. A separate actor — the Comprador menu-bar app, via
// NSMetadataQuery + direct read of the source file on the user's Mac —
// later POSTs the remainder bytes to /_comprador/resume, and the store
// stitches the upload back together. Once the bytes-on-disk match the
// expected size, the upload is "ready" and the WebDAV layer commits it
// to MTP.
//
// See docs/RESUMABLE-UPLOADS.md for the architecture and rationale.
package resume

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// newID returns a 16-byte random hex string used as a session ID. We
// don't pull in google/uuid for one function — crypto/rand + hex gives
// us 128 bits of randomness in a filesystem-safe form.
func newID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand should never fail on macOS; if it does, fall back
		// to a timestamp-based pseudo-id rather than panic.
		return fmt.Sprintf("ts%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// SessionMeta is the JSON sidecar describing a pending upload.
//
// It is read on bridge startup to recover sessions across crashes, and
// served to the Swift companion so it knows which source file to look
// up and where to start streaming from.
type SessionMeta struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`           // WebDAV path being uploaded, e.g. /Internal shared storage/Download/foo.mkv
	BaseName      string    `json:"base_name"`      // path.Base(Path) — the filename, used for source discovery
	ExpectedSize  int64     `json:"expected_size"`  // X-Expected-Entity-Length from the original PUT
	ReceivedSize  int64     `json:"received_size"`  // bytes currently on disk in the .partial file
	StartedAt     time.Time `json:"started_at"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
}

// Store manages the on-disk pending-uploads directory.
//
// Concurrency: a single `sync.Mutex` protects all session bookkeeping.
// This is fine for the bridge's expected load (handful of in-flight
// uploads max). The disk I/O for body bytes is unlocked — append goes
// directly to the .partial file via os.OpenFile/O_APPEND.
type Store struct {
	dir      string
	mu       sync.Mutex
	sessions map[string]*SessionMeta // keyed by session ID
}

// NewStore creates the pending-uploads directory under the user's
// Application Support folder and loads any pre-existing sessions left
// over from a previous bridge run.
//
// Default location: $HOME/Library/Application Support/Comprador/pending/
// Override with $COMPRADOR_PENDING_DIR for tests.
func NewStore() (*Store, error) {
	dir := os.Getenv("COMPRADOR_PENDING_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resume.NewStore: %w", err)
		}
		dir = filepath.Join(home, "Library", "Application Support", "Comprador", "pending")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("resume.NewStore mkdir %s: %w", dir, err)
	}

	s := &Store{dir: dir, sessions: map[string]*SessionMeta{}}
	if err := s.loadPending(); err != nil {
		return nil, fmt.Errorf("resume.NewStore load: %w", err)
	}
	return s, nil
}

// CreateFromPartial registers a new session for a truncated upload and
// writes the partial body to disk. Returns the session ID, which is also
// embedded in the on-disk filename pair (<id>.partial + <id>.json).
//
// `body` is the bytes the WebDAV PUT actually delivered before
// webdavfs gave up. They become the prefix of the eventual full file.
func (s *Store) CreateFromPartial(uploadPath string, expectedSize int64, body io.Reader, bodyLen int64) (*SessionMeta, error) {
	id := newID()
	now := time.Now()
	meta := &SessionMeta{
		ID:            id,
		Path:          uploadPath,
		BaseName:      filepath.Base(uploadPath),
		ExpectedSize:  expectedSize,
		ReceivedSize:  bodyLen,
		StartedAt:     now,
		LastUpdatedAt: now,
	}

	partialPath := s.partialFile(id)
	f, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("resume.CreateFromPartial open: %w", err)
	}
	written, err := io.Copy(f, body)
	closeErr := f.Close()
	if err != nil {
		os.Remove(partialPath)
		return nil, fmt.Errorf("resume.CreateFromPartial write: %w", err)
	}
	if closeErr != nil {
		os.Remove(partialPath)
		return nil, fmt.Errorf("resume.CreateFromPartial close: %w", closeErr)
	}
	if written != bodyLen {
		os.Remove(partialPath)
		return nil, fmt.Errorf("resume.CreateFromPartial: wrote %d, expected %d", written, bodyLen)
	}

	if err := s.writeMeta(meta); err != nil {
		os.Remove(partialPath)
		return nil, err
	}

	s.mu.Lock()
	s.sessions[id] = meta
	s.mu.Unlock()
	return meta, nil
}

// Append streams more bytes into an existing session's partial file. The
// caller is responsible for sourcing only the bytes that come after the
// session's current ReceivedSize (i.e., the Swift companion reads the
// source file at offset ReceivedSize).
//
// Returns the updated total size on disk after the append.
func (s *Store) Append(id string, body io.Reader) (int64, error) {
	s.mu.Lock()
	meta, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("resume.Append: unknown session %s", id)
	}

	partialPath := s.partialFile(id)
	f, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("resume.Append open: %w", err)
	}
	written, err := io.Copy(f, body)
	closeErr := f.Close()
	if err != nil {
		return 0, fmt.Errorf("resume.Append write: %w", err)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("resume.Append close: %w", closeErr)
	}

	s.mu.Lock()
	meta.ReceivedSize += written
	meta.LastUpdatedAt = time.Now()
	total := meta.ReceivedSize
	s.mu.Unlock()

	if err := s.writeMeta(meta); err != nil {
		return total, err
	}
	return total, nil
}

// Get returns a snapshot of session metadata.
func (s *Store) Get(id string) (*SessionMeta, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	cp := *meta
	return &cp, true
}

// List returns metadata for every active session, in unspecified order.
// Used by Swift companion to enumerate work it needs to do, and by debug
// endpoints.
func (s *Store) List() []SessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionMeta, 0, len(s.sessions))
	for _, m := range s.sessions {
		out = append(out, *m)
	}
	return out
}

// IsComplete reports whether a session has received all its expected
// bytes. The WebDAV layer should call this after every Append; on true,
// it can read the partial file and commit to MTP.
func (s *Store) IsComplete(id string) bool {
	meta, ok := s.Get(id)
	if !ok {
		return false
	}
	return meta.ReceivedSize >= meta.ExpectedSize
}

// PartialPath returns the absolute path to the on-disk body file for a
// session. The caller can `os.Open` it and stream into MTP without
// holding the store lock.
func (s *Store) PartialPath(id string) string {
	return s.partialFile(id)
}

// Cleanup deletes both the .partial body and the .json sidecar, and
// drops the session from the in-memory map. Called after a successful
// MTP commit, or when an upload is abandoned.
func (s *Store) Cleanup(id string) error {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()

	var firstErr error
	if err := os.Remove(s.partialFile(id)); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}
	if err := os.Remove(s.metaFile(id)); err != nil && !os.IsNotExist(err) {
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ------------ internal helpers -------------------------------------------

func (s *Store) partialFile(id string) string { return filepath.Join(s.dir, id+".partial") }
func (s *Store) metaFile(id string) string    { return filepath.Join(s.dir, id+".json") }

func (s *Store) writeMeta(m *SessionMeta) error {
	tmp := s.metaFile(m.ID) + ".tmp"
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("resume.writeMeta marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("resume.writeMeta tmp: %w", err)
	}
	if err := os.Rename(tmp, s.metaFile(m.ID)); err != nil {
		return fmt.Errorf("resume.writeMeta rename: %w", err)
	}
	return nil
}

// loadPending populates s.sessions from existing .json sidecars on disk.
// Skips sidecars whose .partial file is missing — they're orphans from
// a previous crash that didn't commit a partial body before sidecar
// landed. (The opposite — partial without sidecar — is unrecoverable
// and gets garbage-collected on next Cleanup.)
func (s *Store) loadPending() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !hasSuffix(name, ".json") {
			continue
		}
		id := name[:len(name)-len(".json")]
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		// Sanity: partial must exist and match recorded size.
		st, err := os.Stat(s.partialFile(id))
		if err != nil {
			continue
		}
		if st.Size() != meta.ReceivedSize {
			meta.ReceivedSize = st.Size()
			_ = s.writeMeta(&meta)
		}
		s.sessions[id] = &meta
	}
	return nil
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}
