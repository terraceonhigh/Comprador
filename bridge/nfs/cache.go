package nfs

import (
	"log"
	"os"
	"sync"
	"time"

	billy "github.com/go-git/go-billy/v5"

	"comprador/bridge/mtp"
)

const cacheEntryTTL = 30 * time.Second

// prefetchChunkSize bounds each libmtp partial-object request issued by
// cache.download. Derivation: per the empirical probe (commit 32ee45cd
// against Xperia XQ-BT52 and Pixel 6), MTP transaction setup costs ~17 ms;
// at ~30 MB/s transfer rate, 16 MB amortizes the setup to ~3 % overhead
// while keeping worst-case per-chunk wall time at ~600 ms — well inside
// macOS NFSv3's timeo=10 first-timeout window. The chunked prefetch loop
// yields the session goroutine between chunks via the PriorityLow lane,
// so a high-priority NFS RPC arriving mid-prefetch waits at most one
// chunk's latency. See docs/PLAN-PREFETCH-REDESIGN.md "Amortization math"
// for the full table.
const prefetchChunkSize = 16 * 1024 * 1024

// downloadCache keeps MTP files as temp files on disk so that go-nfs's per-RPC
// fs.Open / fh.ReadAt(offset) / fh.Close pattern does not re-download the same
// file for every 32KB chunk. Entries expire after cacheEntryTTL of inactivity.
type downloadCache struct {
	mu      sync.Mutex
	entries map[uint32]*cacheEntry
}

type cacheEntry struct {
	tmp     *os.File
	name    string
	lastUse time.Time
	// ready is closed once the download completes (success or failure).
	ready chan struct{}
	err   error
}

func newDownloadCache() *downloadCache {
	return &downloadCache{entries: make(map[uint32]*cacheEntry)}
}

// open returns a cachedHandle for the given MTP object. If the file is not yet
// cached it downloads it; if a download is already in progress it waits. Stale
// cache entries (unused for cacheEntryTTL) are evicted lazily on each call.
//
// The download itself runs in a goroutine so beginPrefetch (the async sibling
// of this method) can share the same machinery; this method just additionally
// blocks on entry.ready before returning.
func (c *downloadCache) open(name string, id uint32, size uint64, session *mtp.Session) (*cachedHandle, error) {
	c.mu.Lock()
	c.evictStale()

	entry, exists := c.entries[id]
	if !exists {
		entry = &cacheEntry{name: name, ready: make(chan struct{})}
		c.entries[id] = entry
		c.mu.Unlock()
		go c.download(entry, id, size, session)
	} else {
		c.mu.Unlock()
	}

	<-entry.ready
	if entry.err != nil {
		return nil, entry.err
	}
	c.mu.Lock()
	entry.lastUse = time.Now()
	c.mu.Unlock()
	return &cachedHandle{name: name, entry: entry, cache: c}, nil
}

// beginPrefetch starts (or rejoins) an asynchronous download for the given
// MTP object and reports whether the file is already ready for synchronous
// read. Used by the vendored go-nfs onRead JUKEBOX path: when an NFS READ
// arrives for a file above the size threshold, we want to return JUKEBOX
// fast and start the libmtp download in the background, so the client's
// retry (macOS's NFS client backs off at 4 s / 8 s / 16 s / 30 s) eventually
// finds a populated cache and gets real bytes.
//
// Returns true if the entry is in the ready state (download has completed
// successfully). Returns false in all other cases — entry doesn't exist
// (new download just started), download still running, or download failed
// (failed entries get evicted on next call so the next retry restarts).
//
// Never blocks. Safe to call concurrently from many goroutines for the
// same id — the entry-creation race is serialized by c.mu.
func (c *downloadCache) beginPrefetch(name string, id uint32, size uint64, session *mtp.Session) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictStale()
	entry, exists := c.entries[id]
	if !exists {
		entry = &cacheEntry{name: name, ready: make(chan struct{})}
		c.entries[id] = entry
		log.Printf("cache.beginPrefetch START: name=%q id=%d size=%d", name, id, size)
		go c.download(entry, id, size, session)
		return false
	}
	select {
	case <-entry.ready:
		return entry.err == nil
	default:
		return false
	}
}

// download runs the MTP transfer that populates entry. Must be called
// without c.mu held. Closes entry.ready when done (whether success or
// failure).
//
// Chunked-yield design (PLAN-PREFETCH-REDESIGN.md Step 3): instead of a
// single OpGetFile that locks the libmtp session goroutine for the
// duration of the entire transfer, this loops over the object in
// prefetchChunkSize strides via OpGetPartial at PriorityLow. Between
// chunks the session goroutine's priority pump drains any PriorityHigh
// (real NFS RPC) requests that have arrived, so a Finder browse during
// prefetch sees ~600 ms lag per chunk rather than the multi-minute
// session-goroutine lock that produced the 2026-05-18 morning cascade.
//
// Termination is one rule with two complementary stops: keep going
// while offset < size, and break when libmtp returns zero bytes for
// a chunk. The first stops cleanly on known sizes; the second
// handles size==0 metadata or stale-size cases (phone-side mutation
// between Stat and read) without a separate code path.
func (c *downloadCache) download(entry *cacheEntry, id uint32, size uint64, session *mtp.Session) {
	t0 := time.Now()
	log.Printf("cache.download START: name=%q id=%d size=%d", entry.name, id, size)
	defer func() {
		if entry.err != nil {
			log.Printf("cache.download END (FAIL %v) name=%q dt=%s", entry.err, entry.name, time.Since(t0))
		} else {
			log.Printf("cache.download END (OK) name=%q dt=%s", entry.name, time.Since(t0))
		}
	}()
	tmp, err := os.CreateTemp("", "comprador-mtp-*")
	if err != nil {
		c.mu.Lock()
		delete(c.entries, id)
		c.mu.Unlock()
		entry.err = err
		close(entry.ready)
		return
	}

	fail := func(e error) {
		tmp.Close()
		os.Remove(tmp.Name())
		c.mu.Lock()
		delete(c.entries, id)
		c.mu.Unlock()
		entry.err = e
		close(entry.ready)
	}

	var offset uint64
	for {
		chunkMax := uint64(prefetchChunkSize)
		if size > 0 {
			if offset >= size {
				break
			}
			if offset+chunkMax > size {
				chunkMax = size - offset
			}
		}

		resp := session.Do(mtp.MTPRequest{
			Op:       mtp.OpGetPartial,
			Priority: mtp.PriorityLow,
			ObjectID: id,
			Offset:   offset,
			Size:     chunkMax,
			Writer:   tmp,
		})
		if resp.Err != nil {
			fail(resp.Err)
			return
		}
		if resp.BytesRead == 0 {
			break
		}
		offset += uint64(resp.BytesRead)
	}

	entry.tmp = tmp
	entry.lastUse = time.Now()
	close(entry.ready)
}

// evictStale removes entries that have not been accessed within cacheEntryTTL.
// Must be called with c.mu held.
func (c *downloadCache) evictStale() {
	cutoff := time.Now().Add(-cacheEntryTTL)
	for id, e := range c.entries {
		// Don't evict entries still downloading.
		select {
		case <-e.ready:
		default:
			continue
		}
		if e.err != nil || e.lastUse.Before(cutoff) {
			if e.tmp != nil {
				e.tmp.Close()
				os.Remove(e.tmp.Name())
			}
			delete(c.entries, id)
		}
	}
}

// cachedHandle is a read-only billy.File backed by a shared cached temp file.
// It holds its own sequential read position so that concurrent handles for the
// same object do not interfere. ReadAt bypasses the position entirely.
type cachedHandle struct {
	name  string
	entry *cacheEntry
	cache *downloadCache
	pos   int64
}

func (h *cachedHandle) Name() string { return h.name }

func (h *cachedHandle) ReadAt(p []byte, off int64) (int, error) {
	return h.entry.tmp.ReadAt(p, off)
}

func (h *cachedHandle) Read(p []byte) (int, error) {
	n, err := h.entry.tmp.ReadAt(p, h.pos)
	h.pos += int64(n)
	return n, err
}

func (h *cachedHandle) Seek(offset int64, whence int) (int64, error) {
	info, err := h.entry.tmp.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	var abs int64
	switch whence {
	case 0:
		abs = offset
	case 1:
		abs = h.pos + offset
	case 2:
		abs = size + offset
	}
	if abs < 0 {
		abs = 0
	}
	h.pos = abs
	return abs, nil
}

func (h *cachedHandle) Write(_ []byte) (int, error) { return 0, billy.ErrReadOnly }
func (h *cachedHandle) Truncate(_ int64) error       { return billy.ErrReadOnly }
func (h *cachedHandle) Lock() error                  { return nil }
func (h *cachedHandle) Unlock() error                { return nil }

func (h *cachedHandle) Close() error {
	h.cache.mu.Lock()
	h.entry.lastUse = time.Now()
	h.cache.mu.Unlock()
	return nil
}
