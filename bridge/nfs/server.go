package nfs

import (
	"net"

	gonfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"

	"comprador/bridge/mtp"
)

// readJukeboxThreshold is the file size above which onRead returns
// NFS3ERR_JUKEBOX immediately rather than triggering a synchronous
// full-file libmtp download. See docs/PLAN-NFS-READ.md and MISTAKES
// entry §NFS pivot 4 for the empirical receipts. 50 MB is comfortably
// inside macOS's NFS RPC timeout window (~20–30 s) at typical USB-MTP
// rates (21 MB/s); above that, the synchronous download blows the
// timeout and Finder shows "Server connections interrupted."
const readJukeboxThreshold int64 = 50 * 1024 * 1024

// Serve starts an NFS server backed by the given MTP session on the provided listener.
// Blocks until the listener is closed. Returns any non-close error.
func Serve(listener net.Listener, session *mtp.Session) error {
	fs := NewMTPFileSystem(session)
	handler := newMTPNFSHandler(fs, session)
	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)

	// Wire JUKEBOX-on-threshold into the patched go-nfs onRead. The size
	// lookup runs in ObjectMap (in-memory, no MTP round-trip) so it
	// stays cheap even when Finder bulk-probes a directory.
	gonfs.ReadSyncThreshold = readJukeboxThreshold
	gonfs.ReadJukeboxSizeFn = func(p string) (int64, bool) {
		// p is fs.Join-form: no leading slash, "/" separator. Convert
		// to our ObjectMap form.
		cp := cleanPath(p)
		// Synthetic sentinels (e.g. /.metadata_never_index) are tiny
		// and should never JUKEBOX.
		if _, ok := sentinelInfo(cp); ok {
			return 0, true
		}
		// Staged writes: size of the in-progress file is whatever the
		// staging temp has so far. Don't JUKEBOX a staged read.
		if sf := fs.writes.get(cp); sf != nil {
			if info, err := sf.stat(); err == nil {
				return info.Size(), true
			}
		}
		// MTP-resident: read size from the in-memory ObjectMap.
		if meta, ok := session.Objects.GetByPath(cp); ok {
			return int64(meta.Size), true
		}
		// Unknown path: don't JUKEBOX, let the existing not-found
		// handling fire downstream.
		return 0, false
	}

	// Async prefetch hook: when onRead is about to JUKEBOX a large
	// file, kick off the libmtp download in the background. If a
	// prior prefetch has already populated the cache, report
	// ready=true and onRead drops the JUKEBOX and reads from cache.
	// Without this, JUKEBOX-only causes apps that do straight
	// read() syscalls (media players, cat, md5sum) to hang
	// indefinitely; with it, the same apps wait the libmtp
	// download duration (~7 min for a 9 GB file at USB-MTP rate)
	// and then receive bytes. See MISTAKES.md entry §NFS pivot 4.
	gonfs.ReadJukeboxBeginFn = func(p string) bool {
		cp := cleanPath(p)
		// Sentinels and staged writes are always "ready" — they
		// shouldn't even reach this hook (ReadJukeboxSizeFn reports
		// their size as 0 and below threshold), but defensive.
		if _, ok := sentinelInfo(cp); ok {
			return true
		}
		if sf := fs.writes.get(cp); sf != nil {
			_ = sf
			return true
		}
		meta, ok := session.Objects.GetByPath(cp)
		if !ok || meta.IsDir {
			return false
		}
		return fs.cache.beginPrefetch(meta.Name, meta.ID, session)
	}

	return gonfs.Serve(listener, cacheHelper)
}
