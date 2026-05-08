package nfs

import (
	"os"
	"sync"
	"time"

	billy "github.com/go-git/go-billy/v5"

	"comprador/bridge/mtp"
)

const cacheEntryTTL = 30 * time.Second

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
func (c *downloadCache) open(name string, id uint32, session *mtp.Session) (*cachedHandle, error) {
	c.mu.Lock()
	c.evictStale()

	entry, exists := c.entries[id]
	if !exists {
		entry = &cacheEntry{name: name, ready: make(chan struct{})}
		c.entries[id] = entry
		c.mu.Unlock()
		c.download(entry, id, session)
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

// download runs the MTP transfer that populates entry. Must be called without
// c.mu held. Closes entry.ready when done (whether success or failure).
func (c *downloadCache) download(entry *cacheEntry, id uint32, session *mtp.Session) {
	tmp, err := os.CreateTemp("", "comprador-mtp-*")
	if err != nil {
		c.mu.Lock()
		delete(c.entries, id)
		c.mu.Unlock()
		entry.err = err
		close(entry.ready)
		return
	}
	resp := session.Do(mtp.MTPRequest{
		Op:       mtp.OpGetFile,
		ObjectID: id,
		Writer:   tmp,
	})
	if resp.Err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		c.mu.Lock()
		delete(c.entries, id)
		c.mu.Unlock()
		entry.err = resp.Err
		close(entry.ready)
		return
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
