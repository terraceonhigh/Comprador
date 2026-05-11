package mtp

import (
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
)

// ObjectMeta holds POSIX-like metadata for an MTP object.
type ObjectMeta struct {
	ID        uint32
	ParentID  uint32
	StorageID uint32
	Name      string
	Path      string
	Size      uint64
	ModTime   time.Time
	IsDir     bool
}

// ObjectMap maintains a bidirectional mapping between POSIX paths and MTP object IDs.
// Directories are lazily populated: a directory exists in the map once its parent
// has been listed, but its children are only fetched when ListDir is called.
type ObjectMap struct {
	mu         sync.RWMutex
	byPath     map[string]*ObjectMeta
	byID       map[uint32]*ObjectMeta
	populated  map[string]bool // tracks which directories have had their children fetched
}

func NewObjectMap() *ObjectMap {
	return &ObjectMap{
		byPath:    make(map[string]*ObjectMeta),
		byID:      make(map[uint32]*ObjectMeta),
		populated: make(map[string]bool),
	}
}

func (m *ObjectMap) Put(meta *ObjectMeta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byPath[meta.Path] = meta
	m.byID[meta.ID] = meta
}

func (m *ObjectMap) Remove(objPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta, ok := m.byPath[objPath]; ok {
		delete(m.byPath, objPath)
		delete(m.byID, meta.ID)
	}
	// Also invalidate this directory's populated status
	delete(m.populated, objPath)
}

func (m *ObjectMap) GetByPath(p string) (*ObjectMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta, ok := m.byPath[p]
	return meta, ok
}

func (m *ObjectMap) GetByID(id uint32) (*ObjectMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta, ok := m.byID[id]
	return meta, ok
}

// InvalidateDir marks a directory as needing re-enumeration from the device.
func (m *ObjectMap) InvalidateDir(dirPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.populated, strings.TrimSuffix(dirPath, "/"))
}

// IsPopulated returns whether a directory's children have been fetched.
func (m *ObjectMap) IsPopulated(dirPath string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.populated[dirPath]
}

// MarkPopulated marks a directory as having had its children fetched.
func (m *ObjectMap) MarkPopulated(dirPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.populated[dirPath] = true
}

// ListChildren returns cached children of a directory (does not fetch from device).
func (m *ObjectMap) ListChildren(dirPath string) []*ObjectMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dirPath = strings.TrimSuffix(dirPath, "/")
	prefix := dirPath + "/"
	var children []*ObjectMeta
	for p, meta := range m.byPath {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		// Only direct children: no further slashes after the prefix
		rest := p[len(prefix):]
		if rest != "" && !strings.Contains(rest, "/") {
			children = append(children, meta)
		}
	}
	return children
}

// MTPOp identifies the type of MTP operation.
type MTPOp int

const (
	OpGetFile MTPOp = iota
	OpSendFile
	OpDelete
	OpCreateFolder
	OpListDir // lazy enumeration of a single directory
	OpRefreshStorages // re-query LIBMTP_Get_Storage to refresh free/max bytes per storage
)

// MTPRequest is sent to the session goroutine.
type MTPRequest struct {
	Op        MTPOp
	ObjectID  uint32
	ParentID  uint32
	StorageID uint32
	Name      string
	Size      uint64
	Path      string // for OpListDir: the directory path
	Writer    io.Writer
	Reader    io.Reader
	Response  chan MTPResponse
}

// MTPResponse is returned from the session goroutine.
type MTPResponse struct {
	Entries  []*ObjectMeta
	ObjectID uint32
	Err      error
}

// Session owns the MTP device and serialises all operations.
type Session struct {
	device   *Device
	Objects  *ObjectMap
	// storages is the cached storage list. Snapshotted at session init and
	// refreshed via OpRefreshStorages (e.g. on each FSStat call so Finder sees
	// up-to-date per-storage free numbers). Protected by storagesMu because
	// the session goroutine writes it (during refresh) while NFS handler
	// goroutines read it via TotalBytes / FreeBytes / StorageForPath.
	storages   []Storage
	storagesMu sync.RWMutex
	requests   chan MTPRequest
	done       chan struct{}
}

// TotalBytes returns the sum of MaxCapacity across all storages on the device.
// Used as the aggregate fallback when FSStat is called at the mount root or
// against an unknown path; per-storage routing (via StorageForPath) is
// preferred for any path under a specific storage subtree so that Finder's
// "X GB available" string is accurate for the storage the user is browsing.
func (s *Session) TotalBytes() uint64 {
	s.storagesMu.RLock()
	defer s.storagesMu.RUnlock()
	var n uint64
	for _, st := range s.storages {
		n += st.MaxBytes
	}
	return n
}

// FreeBytes returns the sum of FreeSpaceInBytes across all storages.
// Aggregate fallback; per-storage values come through StorageForPath, which
// the bridge's FSStat handler uses preferentially. RefreshStorages should
// be called before reading these if up-to-date numbers matter — the slice
// is mutated only inside the session goroutine.
func (s *Session) FreeBytes() uint64 {
	s.storagesMu.RLock()
	defer s.storagesMu.RUnlock()
	var n uint64
	for _, st := range s.storages {
		n += st.FreeBytes
	}
	return n
}

// StorageForPath resolves a path's owning Storage, or returns nil if the path
// is at the mount root or under an unknown first segment. The first non-empty
// component of `segments` is matched against `sanitizeName(st.Description)`
// for each known storage — same form initStorages writes into the ObjectMap
// at `"/" + sanitizeName(st.Description)`.
//
// Returns nil for root-level queries (so handlers can fall back to aggregate
// reporting without surfacing an error to the NFS client).
func (s *Session) StorageForPath(segments []string) *Storage {
	first := ""
	for _, seg := range segments {
		if seg != "" {
			first = seg
			break
		}
	}
	if first == "" {
		return nil
	}
	s.storagesMu.RLock()
	defer s.storagesMu.RUnlock()
	for i := range s.storages {
		if sanitizeName(s.storages[i].Description) == first {
			return &s.storages[i]
		}
	}
	return nil
}

// RefreshStorages re-queries libmtp for the device's storage list and replaces
// the cached slice atomically. Synchronous: blocks the caller until the session
// goroutine has refreshed. Used on each FSStat call so Finder sees free-space
// numbers that decrement after a write rather than the snapshot taken at
// session open. libmtp's LIBMTP_Get_Storage refreshes all storages in one call;
// per-storage refresh isn't exposed.
func (s *Session) RefreshStorages() error {
	resp := s.Do(MTPRequest{Op: OpRefreshStorages})
	return resp.Err
}

// NewSession opens a device and populates the root-level storage entries.
func NewSession() (*Session, error) {
	dev, err := DetectDevice()
	if err != nil {
		return nil, err
	}

	s := &Session{
		device:   dev,
		Objects:  NewObjectMap(),
		requests: make(chan MTPRequest, 16),
		done:     make(chan struct{}),
	}

	if err := s.initStorages(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("initializing storages: %w", err)
	}

	go s.run()
	return s, nil
}

// DeviceName returns the friendly name of the connected device.
func (s *Session) DeviceName() string {
	return s.device.FriendlyName()
}

// Close shuts down the session goroutine and releases the device.
func (s *Session) Close() {
	close(s.requests)
	<-s.done
	s.device.Close()
}

// Do sends a request to the session goroutine and waits for the response.
func (s *Session) Do(req MTPRequest) MTPResponse {
	req.Response = make(chan MTPResponse, 1)
	s.requests <- req
	return <-req.Response
}

// EnsurePopulated makes sure the children of dirPath have been fetched from the device.
// This is safe to call from any goroutine — the actual MTP call runs on the session goroutine.
func (s *Session) EnsurePopulated(dirPath string) {
	if s.Objects.IsPopulated(dirPath) {
		return
	}
	s.Do(MTPRequest{Op: OpListDir, Path: dirPath})
}

// EnsureInMap walks down from root, populating each ancestor directory
// in turn until `dirPath` itself is in the object map. Used by the
// resumable-upload commit path, where a session may need to commit a
// path that hasn't been browsed by Finder this run — without this walk,
// `GetByPath(parent)` would miss and the commit would fail with
// "parent not in object map" even though the directory exists on the
// device.
//
// Returns true if dirPath ended up in the map. Callers should still
// `GetByPath(dirPath)` to fetch the meta — this is just a populate
// driver, not a lookup.
func (s *Session) EnsureInMap(dirPath string) bool {
	if dirPath == "/" || dirPath == "" {
		return true // root always exists
	}
	if _, ok := s.Objects.GetByPath(dirPath); ok {
		return true
	}
	// Populate the parent so this entry shows up as one of its children.
	parent := dirPath[:strings.LastIndex(dirPath, "/")]
	if parent == "" {
		parent = "/"
	}
	if !s.EnsureInMap(parent) {
		return false
	}
	s.EnsurePopulated(parent)
	_, ok := s.Objects.GetByPath(dirPath)
	return ok
}

func (s *Session) run() {
	defer close(s.done)
	for req := range s.requests {
		resp := s.dispatch(req)
		req.Response <- resp
	}
}

func (s *Session) dispatch(req MTPRequest) MTPResponse {
	switch req.Op {
	case OpGetFile:
		err := s.device.GetFileToWriter(req.ObjectID, req.Writer)
		return MTPResponse{Err: err}
	case OpSendFile:
		parentID := s.resolveParentID(req.ParentID, req.StorageID)
		id, err := s.device.SendFileFromReader(parentID, req.StorageID, req.Name, req.Size, req.Reader)
		return MTPResponse{ObjectID: id, Err: err}
	case OpDelete:
		err := s.device.DeleteObject(req.ObjectID)
		return MTPResponse{Err: err}
	case OpCreateFolder:
		parentID := s.resolveParentID(req.ParentID, req.StorageID)
		id, err := s.device.CreateFolder(req.Name, parentID, req.StorageID)
		return MTPResponse{ObjectID: id, Err: err}
	case OpListDir:
		entries := s.populateDir(req.Path)
		return MTPResponse{Entries: entries}
	case OpRefreshStorages:
		storages, err := s.device.GetStorages()
		if err != nil {
			return MTPResponse{Err: err}
		}
		s.storagesMu.Lock()
		s.storages = storages
		s.storagesMu.Unlock()
		return MTPResponse{}
	default:
		return MTPResponse{Err: fmt.Errorf("unknown op: %d", req.Op)}
	}
}

// initStorages fetches storage list and registers them as top-level directories.
// Runs in NewSession before the session goroutine starts, so no concurrent
// readers exist yet; the lock here is for consistency with the access pattern.
func (s *Session) initStorages() error {
	storages, err := s.device.GetStorages()
	if err != nil {
		return err
	}
	s.storagesMu.Lock()
	s.storages = storages
	s.storagesMu.Unlock()

	log.Printf("Found %d storage(s)", len(storages))
	for _, st := range storages {
		log.Printf("  Storage %d: %s (%.1f GB free / %.1f GB total)",
			st.ID, st.Description,
			float64(st.FreeBytes)/1e9, float64(st.MaxBytes)/1e9)

		storagePath := "/" + sanitizeName(st.Description)
		s.Objects.Put(&ObjectMeta{
			ID:        st.ID,
			StorageID: st.ID,
			Name:      st.Description,
			Path:      storagePath,
			IsDir:     true,
			ModTime:   time.Now(),
		})
	}

	// Mark root as populated (its children are the storages)
	s.Objects.MarkPopulated("/")
	return nil
}

// populateDir fetches children of a directory from the device and caches them.
// Must be called from the session goroutine.
func (s *Session) populateDir(dirPath string) []*ObjectMeta {
	dirPath = strings.TrimSuffix(dirPath, "/")

	if s.Objects.IsPopulated(dirPath) {
		return s.Objects.ListChildren(dirPath)
	}

	meta, ok := s.Objects.GetByPath(dirPath)
	if !ok || !meta.IsDir {
		return nil
	}

	// For storage roots, parentID for enumeration is FilesAndFoldersRoot
	mtpParentID := meta.ID
	storageID := meta.StorageID

	// Check if this is a storage root (its ID == its StorageID and parentID is 0)
	// For storage entries, we enumerate with the root constant
	if meta.ID == meta.StorageID {
		mtpParentID = FilesAndFoldersRoot
	}

	entries, err := s.device.GetFilesAndFolders(storageID, mtpParentID)
	if err != nil {
		// Don't mark populated — a transient PTP I/O error (phone screen
		// asleep, USB renumeration mid-flight, kernel-driver collision)
		// would otherwise lock the cache to "empty directory" until the
		// bridge process restarts. By leaving the path unpopulated, the
		// next access retries the enumeration once the device recovers.
		log.Printf("Lazy enumerate %s: error, leaving unpopulated: %v", dirPath, err)
		return nil
	}
	log.Printf("Lazy enumerate %s: %d entries", dirPath, len(entries))

	var result []*ObjectMeta
	for _, e := range entries {
		objPath := dirPath + "/" + sanitizeName(e.Name)
		obj := &ObjectMeta{
			ID:        e.ID,
			ParentID:  e.ParentID,
			StorageID: e.StorageID,
			Name:      e.Name,
			Path:      objPath,
			Size:      e.Size,
			ModTime:   time.Unix(e.ModTime, 0),
			IsDir:     e.IsFolder,
		}
		s.Objects.Put(obj)
		result = append(result, obj)
	}

	s.Objects.MarkPopulated(dirPath)
	return result
}

// resolveParentID converts our internal parent ID to the MTP parent ID.
// Storage root entries have ID == StorageID, but MTP expects parent_id=0xFFFFFFFF
// for objects at the root of a storage.
func (s *Session) resolveParentID(parentID, storageID uint32) uint32 {
	if parentID == storageID {
		return FilesAndFoldersRoot // 0xFFFFFFFF = root of storage
	}
	return parentID
}

func sanitizeName(name string) string {
	return strings.ReplaceAll(name, "/", "_")
}
