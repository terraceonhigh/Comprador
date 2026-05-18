package nfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	"github.com/willscott/go-nfs-client/nfs/xdr"
)

type nfsReadArgs struct {
	Handle []byte
	Offset uint64
	Count  uint32
}

type nfsReadResponse struct {
	Count uint32
	EOF   uint32
	Data  []byte
}

// MaxRead is the advertised largest buffer the server is willing to read
const MaxRead = 1 << 24

// ReadSyncThreshold is the file size above which onRead returns
// NFS3ERR_JUKEBOX immediately rather than blocking the RPC handler on a
// potentially-slow backend read. The motivating use case is Comprador:
// reads go through libmtp, which has no random-access read and must
// download the entire file before answering even the first 32 KB chunk.
// For a 9 GB file at USB-MTP rates that is ~7 minutes — far longer than
// macOS's NFS RPC timeout (~20-30 s). Returning JUKEBOX (RFC 1813 §2.6)
// for large files lets clients like Finder / QuickLook / Spotlight
// degrade gracefully ("media not ready, retry later") rather than stall
// the whole mount on a single background read.
//
// 0 means no threshold (legacy behavior — always synchronous). Set this
// at process startup from the bridge's main, before serving NFS.
//
// COMPRADOR-PATCH; not upstream.
var ReadSyncThreshold int64 = 0

// ReadJukeboxSizeFn, if non-nil, returns the size of the file at the given
// path without triggering any expensive open / download. Used by onRead to
// short-circuit large-file reads with JUKEBOX before calling fs.Open
// (which in Comprador's case would synchronously download the entire
// file via libmtp just so we could close it and return JUKEBOX).
//
// COMPRADOR-PATCH; not upstream.
var ReadJukeboxSizeFn func(path string) (int64, bool) = nil

// ReadJukeboxBeginFn, if non-nil, is called when onRead decides a file is
// over ReadSyncThreshold. The implementation may kick off an asynchronous
// prefetch so the next retry within macOS's NFS-client backoff window
// (4 s → 8 s → 16 s → 30 s) finds populated cache.
//
// Returns true if the file is now ready for synchronous read — either the
// prefetch completed since the last probe, or the bridge had cached the
// file from prior activity. In that case onRead drops the JUKEBOX and
// proceeds with the normal read path (which will hit the cache fast).
//
// Returns false if the file is still being prefetched (or prefetch just
// started). onRead returns NFS3ERR_JUKEBOX in this case, signaling the
// client to retry after a delay.
//
// Without this hook, onRead returns JUKEBOX for every probe indefinitely,
// which is safe for clients with their own retry budgets (Finder /
// QuickLook surface a dismissable alert) but causes hard hangs in clients
// that issue direct read() syscalls (media players, cat, md5sum). With
// this hook, those direct-read clients get bytes after the prefetch
// completes — slow (multi-minute on a multi-GB file via USB-MTP) but
// not infinite.
//
// COMPRADOR-PATCH; not upstream.
var ReadJukeboxBeginFn func(path string) (ready bool) = nil

func onRead(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = opAttrErrorFormatter
	var obj nfsReadArgs
	err := xdr.Read(w.req.Body, &obj)
	if err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}
	fs, path, err := userHandle.FromHandle(obj.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}

	// COMPRADOR-PATCH: for files above the configured threshold,
	// return JUKEBOX immediately while kicking off an async prefetch
	// via ReadJukeboxBeginFn. If a prior prefetch has already
	// populated the cache (ready=true), fall through to the normal
	// read path; otherwise return JUKEBOX so the client retries
	// after its backoff. See ReadSyncThreshold / ReadJukeboxBeginFn
	// docs above.
	if ReadSyncThreshold > 0 && ReadJukeboxSizeFn != nil {
		fullPath := fs.Join(path...)
		if sz, ok := ReadJukeboxSizeFn(fullPath); ok && sz > ReadSyncThreshold {
			ready := false
			if ReadJukeboxBeginFn != nil {
				ready = ReadJukeboxBeginFn(fullPath)
			}
			if !ready {
				Log.Infof("READ JUKEBOX: path=%q size=%d threshold=%d", fullPath, sz, ReadSyncThreshold)
				return &NFSStatusError{NFSStatusJukebox, errors.New("media not ready: file larger than ReadSyncThreshold")}
			}
			Log.Infof("READ prefetched-cache-hit: path=%q size=%d", fullPath, sz)
		}
	}

	fh, err := fs.Open(fs.Join(path...))
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		return &NFSStatusError{NFSStatusAccess, err}
	}
	defer fh.Close()

	resp := nfsReadResponse{}
	setEOF := false

	fullPath := fs.Join(path...)
	info, err := fs.Stat(fullPath)
	if err != nil {
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if int64(obj.Offset) >= info.Size() {
		obj.Count = 0
		setEOF = true
	} else if info.Size()-int64(obj.Offset) <= int64(obj.Count) {
		obj.Count = uint32(uint64(info.Size()) - obj.Offset)
		setEOF = true
	}
	if obj.Count > MaxRead {
		obj.Count = MaxRead
	}
	resp.Data = make([]byte, obj.Count)
	// todo: multiple reads if size isn't full
	cnt, err := fh.ReadAt(resp.Data, int64(obj.Offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return &NFSStatusError{NFSStatusIO, err}
	}
	resp.Count = uint32(cnt)
	resp.Data = resp.Data[:resp.Count]
	if errors.Is(err, io.EOF) || setEOF {
		resp.EOF = 1
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := WritePostOpAttrs(writer, ToFileAttribute(info, fullPath)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := xdr.Write(writer, resp); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
