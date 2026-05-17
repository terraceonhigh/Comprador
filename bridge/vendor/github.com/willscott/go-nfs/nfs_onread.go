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

	// COMPRADOR-PATCH: short-circuit JUKEBOX for files above the
	// configured threshold, before any backend Open/Stat that might
	// trigger a multi-minute download. See ReadSyncThreshold doc.
	if ReadSyncThreshold > 0 && ReadJukeboxSizeFn != nil {
		fullPath := fs.Join(path...)
		if sz, ok := ReadJukeboxSizeFn(fullPath); ok && sz > ReadSyncThreshold {
			Log.Infof("READ JUKEBOX: path=%q size=%d threshold=%d", fullPath, sz, ReadSyncThreshold)
			return &NFSStatusError{NFSStatusJukebox, errors.New("media not ready: file larger than ReadSyncThreshold")}
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
