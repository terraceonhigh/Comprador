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
	mu        sync.RWMutex
	byPath    map[string]*ObjectMeta
	byID      map[uint32]*ObjectMeta
	// populated maps directory paths to the time their children were last
	// fetched from the device. A zero time (or missing key) means never
	// populated. The non-zero time supports a TTL-based staleness check:
	// EnsurePopulated treats anything older than directoryTTL as in need of
	// refresh, so phone-side mutations (the user deletes a file via the
	// phone's Files app) surface on the next directory access through the
	// NFS mount within a couple seconds. See V0.3.3.md item #1 for the
	// motivation.
	populated map[string]time.Time
}

// directoryTTL bounds how long a directory's enumeration is trusted before
// the next access forces a re-fetch from the device. 2 s is the spec from
// V0.3.3.md item #1 — slow enough to amortize the libmtp OpListDir cost
// across burst Finder reads, fast enough that a phone-side delete surfaces
// while the user is still looking at the mount.
const directoryTTL = 2 * time.Second

func NewObjectMap() *ObjectMap {
	return &ObjectMap{
		byPath:    make(map[string]*ObjectMeta),
		byID:      make(map[uint32]*ObjectMeta),
		populated: make(map[string]time.Time),
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

// IsPopulated returns whether a directory's children have been fetched
// (at any point — does not consider freshness). For freshness-aware checks
// use IsFresh.
func (m *ObjectMap) IsPopulated(dirPath string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return !m.populated[dirPath].IsZero()
}

// IsFresh returns whether a directory's enumeration is both populated and
// younger than directoryTTL. Callers should re-enumerate if this returns
// false. Separate from IsPopulated because the bridge's first-access
// behaviour ("never seen this directory; fetch its children") and its
// refresh behaviour ("seen it but cache is stale") want different code
// paths (the latter has to reconcile new vs old entries to surface
// phone-side deletes).
func (m *ObjectMap) IsFresh(dirPath string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := m.populated[dirPath]
	return !t.IsZero() && time.Since(t) < directoryTTL
}

// MarkPopulated marks a directory as having had its children fetched
// (timestamp = now). EnsurePopulated will trust this for up to
// directoryTTL before forcing a re-fetch.
func (m *ObjectMap) MarkPopulated(dirPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.populated[dirPath] = time.Now()
}

// removeRecursive deletes meta at dirPath plus every descendant entry
// (anything with dirPath + "/" as a prefix). Used by the staleness-
// refresh path: when a directory disappears from the device's enumeration
// (phone-side rmdir), all of its cached descendants are orphans and need
// to go too — otherwise a subsequent Stat or Open on a deleted descendant
// returns the cached entry and the bridge tries to read a phone object
// that no longer exists.
//
// Caller must hold m.mu.
func (m *ObjectMap) removeRecursiveLocked(dirPath string) {
	if meta, ok := m.byPath[dirPath]; ok {
		delete(m.byPath, dirPath)
		delete(m.byID, meta.ID)
	}
	delete(m.populated, dirPath)
	prefix := dirPath + "/"
	for p, meta := range m.byPath {
		if strings.HasPrefix(p, prefix) {
			delete(m.byPath, p)
			delete(m.byID, meta.ID)
			delete(m.populated, p)
		}
	}
}

// RemoveRecursive removes a path and all its descendants from the map.
// See removeRecursiveLocked for rationale.
func (m *ObjectMap) RemoveRecursive(dirPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeRecursiveLocked(dirPath)
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
	OpGetPartial // partial-range read for chunked prefetch — see cache.download
	OpSetObjectName // rename an object (file or folder) in place — no copy
)

// Priority controls which lane a request enters in the session goroutine's
// run loop. The zero value (PriorityHigh) preserves pre-priority-queue
// behaviour for callers that don't set it explicitly.
//
// PriorityLow is reserved for background chunked prefetch work introduced by
// docs/PLAN-PREFETCH-REDESIGN.md Step 3 — small libmtp transfers that the
// session goroutine should yield between, so a high-priority NFS RPC
// arriving mid-prefetch waits at most one chunk's worth of latency
// (~600 ms at 16 MB chunks per the empirical probe) rather than the full
// multi-minute download.
type Priority int

const (
	PriorityHigh Priority = iota // default: real NFS RPCs, UI-driven operations
	PriorityLow                  // background prefetch chunks
)

// MTPRequest is sent to the session goroutine.
//
// Field roles by op:
//   OpGetFile        : ObjectID, Writer
//   OpGetPartial     : ObjectID, Offset, Size (chunk maxBytes), Writer
//   OpSendFile       : ParentID, StorageID, Name, Size, Reader
//   OpDelete         : ObjectID
//   OpCreateFolder   : Name, ParentID, StorageID
//   OpSetObjectName  : ObjectID, Name
//   OpListDir        : Path
//   OpRefreshStorages: (no inputs)
type MTPRequest struct {
	Op        MTPOp
	Priority  Priority
	ObjectID  uint32
	ParentID  uint32
	StorageID uint32
	Name      string
	Size      uint64 // OpSendFile: total payload; OpGetPartial: chunk maxBytes
	Offset    uint64 // OpGetPartial: byte offset within the source object
	Path      string // for OpListDir: the directory path
	Writer    io.Writer
	Reader    io.Reader
	Response  chan MTPResponse
}

// MTPResponse is returned from the session goroutine.
//
// BytesRead is populated by OpGetPartial and reports how many bytes
// libmtp actually returned (may be 0 at EOF, or shorter than the
// requested Size near end-of-file). The cache's chunked-prefetch loop
// uses BytesRead == 0 as its EOF signal, complementing the
// offset >= size cap.
type MTPResponse struct {
	Entries   []*ObjectMeta
	ObjectID  uint32
	BytesRead uint32
	Err       error
}

// Session owns the MTP device and serialises all operations.
//
// Requests enter via Do(), which routes to highPri or lowPri based on
// req.Priority. The run loop drains highPri preferentially; lowPri is
// only consumed when highPri is empty (see run() for the canonical
// priority-select pattern). closing is a signal-only channel used by
// Close() to terminate the run loop without closing the data channels —
// closing a data channel that an in-flight Do() is mid-send into would
// panic; the signal pattern keeps the use-after-Close contract enforced
// by convention rather than by panic.
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
	highPri    chan MTPRequest
	lowPri     chan MTPRequest
	closing    chan struct{}
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
// Equivalent to NewSessionForLocation(0): first-detected device.
func NewSession() (*Session, error) {
	return NewSessionForLocation(0)
}

// NewSessionForLocation opens the device matching the given macOS IOKit
// USB Location ID (or the first-detected device if locationID==0) and
// populates the root-level storage entries.
func NewSessionForLocation(locationID uint32) (*Session, error) {
	dev, err := DetectDeviceForLocation(locationID)
	if err != nil {
		return nil, err
	}

	s := &Session{
		device:  dev,
		Objects: NewObjectMap(),
		highPri: make(chan MTPRequest, 16),
		lowPri:  make(chan MTPRequest, 16),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
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
//
// Two contract notes that changed in the Step 2 priority-queue refactor
// (commit f5db97cd):
//
//   1. Calling Do() AFTER Close() is a contract violation and will leak
//      the caller's goroutine on a blocked send. (Pre-refactor: would
//      have panicked on send-to-closed-channel; post-refactor: the data
//      channels are intentionally never closed, see the Session doc
//      comment, so the leak is the silent failure mode.)
//
//   2. Calling Do() that is IN FLIGHT (already on highPri/lowPri but
//      not yet dispatched) when Close() fires will likewise leak the
//      caller's goroutine. The closing signal causes the run loop to
//      return immediately rather than drain pending requests.
//      (Pre-refactor: `for req := range s.requests` would drain
//      buffered requests before exit.) In practice this only matters
//      at process exit, where the leaked goroutines die with the
//      process; the single caller (bridge/main.go:53 deferred) fires
//      at that exact moment, so the change is operationally a no-op.
//
// If a future caller of Close() needs the drain-on-close property,
// add a drain phase here that pumps highPri then lowPri to empty
// before signaling closing.
func (s *Session) Close() {
	close(s.closing)
	<-s.done
	s.device.Close()
}

// Do sends a request to the session goroutine and waits for the response.
// req.Priority routes to the high (default) or low lane.
func (s *Session) Do(req MTPRequest) MTPResponse {
	req.Response = make(chan MTPResponse, 1)
	if req.Priority == PriorityLow {
		s.lowPri <- req
	} else {
		s.highPri <- req
	}
	return <-req.Response
}

// EnsurePopulated makes sure the children of dirPath have been fetched from
// the device and are within directoryTTL of the present. The actual MTP call
// runs on the session goroutine; this method is safe from any caller.
//
// "Fresh" is the load-bearing word: a directory whose enumeration is older
// than directoryTTL gets re-fetched even though we already have its
// children cached. That re-fetch reconciles against phone-side mutations
// (the user deletes a file via the phone's Files app and we surface it in
// Finder within a couple seconds). See V0.3.3.md item #1.
func (s *Session) EnsurePopulated(dirPath string) {
	if s.Objects.IsFresh(dirPath) {
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

// run is the canonical Go priority-select pattern. The outer select
// non-blockingly checks the high-priority lane: if a high-pri request is
// waiting it runs immediately. Only when high-pri is empty does the
// inner blocking select pick from either lane — and when both are ready
// at that instant, Go's random pick may take low-pri, in which case the
// next iteration still runs the high-pri request after at most one
// dispatch's worth of latency. With 16 MB prefetch chunks (~600 ms
// worst case) that latency budget is well inside macOS NFSv3's
// timeo=10 (1 sec) first-timeout window. See docs/PLAN-PREFETCH-REDESIGN.md
// "Amortization math" for the derivation.
func (s *Session) run() {
	defer close(s.done)
	for {
		select {
		case req := <-s.highPri:
			req.Response <- s.dispatch(req)
			continue
		default:
		}
		select {
		case req := <-s.highPri:
			req.Response <- s.dispatch(req)
		case req := <-s.lowPri:
			req.Response <- s.dispatch(req)
		case <-s.closing:
			return
		}
	}
}

func (s *Session) dispatch(req MTPRequest) MTPResponse {
	switch req.Op {
	case OpGetFile:
		err := s.device.GetFileToWriter(req.ObjectID, req.Writer)
		return MTPResponse{Err: err}
	case OpGetPartial:
		data, err := s.device.GetPartialObject(req.ObjectID, req.Offset, uint32(req.Size))
		if err != nil {
			return MTPResponse{Err: err}
		}
		if len(data) > 0 {
			if _, werr := req.Writer.Write(data); werr != nil {
				return MTPResponse{Err: werr}
			}
		}
		return MTPResponse{BytesRead: uint32(len(data))}
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
	case OpSetObjectName:
		err := s.device.SetObjectName(req.ObjectID, req.Name)
		return MTPResponse{Err: err}
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
		log.Printf("  Storage %d: %q → sanitized %q (%.1f GB free / %.1f GB total)",
			st.ID, st.Description, sanitizeName(st.Description),
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

// populateDir fetches children of a directory from the device, caches them,
// and reconciles against any cached state from a previous enumeration that
// has aged past directoryTTL. Must be called from the session goroutine.
//
// Reconciliation: when called against a directory that's already populated
// but stale, the new device-side enumeration is treated as ground truth.
// Entries present in the new enumeration are upserted; entries present in
// the old cache but absent from the new enumeration are removed
// recursively (the user deleted them from the phone). This is the
// mechanism by which phone-side mutations surface in Finder.
func (s *Session) populateDir(dirPath string) []*ObjectMeta {
	dirPath = strings.TrimSuffix(dirPath, "/")

	if s.Objects.IsFresh(dirPath) {
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

	// Build the set of paths present in the new enumeration. We'll use this
	// to find orphans from any prior cached state.
	newPaths := make(map[string]bool, len(entries))
	for _, e := range entries {
		newPaths[dirPath+"/"+sanitizeName(e.Name)] = true
	}

	// wasPopulated captures whether this directory had a prior enumeration the
	// NFS client may already have cached. changed tracks whether this re-fetch
	// altered the child set (an add or a remove) — if so we advance the
	// directory's ModTime at the end so its next GETATTR reports a newer
	// ChangeID and the client invalidates its cached READDIR. Only meaningful
	// when wasPopulated: first-time population has no client cache to bust.
	wasPopulated := s.Objects.IsPopulated(dirPath)
	changed := false

	// Reconcile: anything in the old cache for this directory that the
	// device no longer reports is a phone-side delete; remove it (and any
	// cached descendants) recursively. We only do this when there *was* a
	// prior enumeration — first-time population has no old state to clean.
	if wasPopulated {
		for _, oldChild := range s.Objects.ListChildren(dirPath) {
			if !newPaths[oldChild.Path] {
				log.Printf("Reconcile %s: removing %s (no longer on device)",
					dirPath, oldChild.Path)
				s.Objects.RemoveRecursive(oldChild.Path)
				changed = true
			}
		}
	}

	var result []*ObjectMeta
	for _, e := range entries {
		objPath := dirPath + "/" + sanitizeName(e.Name)
		// A path the device now reports that we had no cached entry for is a
		// phone-side add (e.g. a photo just taken). Flag it so we bump the
		// directory's ModTime below. Only counts as a surfaceable change when
		// the directory was already populated (the client may have it cached).
		if wasPopulated {
			if _, existed := s.Objects.GetByPath(objPath); !existed {
				changed = true
			}
		}
		size := e.Size
		// Android reports filesize=0 for a file recently written over MTP until
		// its media scan finalizes the object (a transient window of seconds to
		// minutes). If we just sent a file, our cached entry holds the true size;
		// a re-enumeration during that window would otherwise clobber it with 0,
		// and VirtualRead trusts Size — serving an empty file (a double-click
		// opens nothing) until the bridge restarts. So when the device reports a
		// file as 0 but we already recorded a non-zero size at this path, keep
		// ours. Keyed on PATH, not object ID: the SendObjectInfo handle stored at
		// commit may differ from the item_id a later enumeration reports, which
		// would make an ID match silently fail. Trade-off: a genuine in-place
		// truncation-to-0 of a same-path object keeps its stale size — rare on
		// MTP, where a rewrite gets a new object.
		if !e.IsFolder && size == 0 {
			if prev, ok := s.Objects.GetByPath(objPath); ok && !prev.IsDir && prev.Size > 0 {
				log.Printf("enumerate reported size 0 for %s, preserving cached %d bytes", objPath, prev.Size)
				size = prev.Size
			}
		}
		obj := &ObjectMeta{
			ID:        e.ID,
			ParentID:  e.ParentID,
			StorageID: e.StorageID,
			Name:      e.Name,
			Path:      objPath,
			Size:      size,
			ModTime:   time.Unix(e.ModTime, 0),
			IsDir:     e.IsFolder,
		}
		s.Objects.Put(obj)
		result = append(result, obj)
	}

	if changed {
		// A child appeared or vanished out-of-band (photo taken, file deleted on
		// the phone). Advance this directory's own ModTime so its next GETATTR
		// reports a newer ChangeID and the NFS client drops its cached listing —
		// otherwise the change stays invisible until a replug. Copy-then-Put so a
		// concurrent reader never observes a torn ModTime (mirrors bumpDirMtime).
		bumped := *meta
		bumped.ModTime = time.Now()
		s.Objects.Put(&bumped)
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
