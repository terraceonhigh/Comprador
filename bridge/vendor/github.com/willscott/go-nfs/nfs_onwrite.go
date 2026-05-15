package nfs

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"

	"github.com/go-git/go-billy/v5"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// writeStability is the level of durability requested with the write
type writeStability uint32

const (
	unstable writeStability = 0
	dataSync writeStability = 1
	fileSync writeStability = 2
)

// DurableSyncer is implemented by billy.File adapters that can synchronously
// flush staged writes to a backing store before returning. Comprador's
// stagingHandle implements this to push the staged temp file to the MTP
// device when the NFS client requests fileSync stability. See
// COMPRADOR-PATCH in onWrite below and the block comment in
// bridge/nfs/write.go for the rationale.
type DurableSyncer interface {
	SyncDurable() error
}

type writeArgs struct {
	Handle []byte
	Offset uint64
	Count  uint32
	How    uint32
	Data   []byte
}

func onWrite(ctx context.Context, w *response, userHandle Handler) error {
	w.errorFmt = wccDataErrorFormatter
	var req writeArgs
	if err := xdr.Read(w.req.Body, &req); err != nil {
		return &NFSStatusError{NFSStatusInval, err}
	}

	fs, path, err := userHandle.FromHandle(req.Handle)
	if err != nil {
		return &NFSStatusError{NFSStatusStale, err}
	}
	if !billy.CapabilityCheck(fs, billy.WriteCapability) {
		return &NFSStatusError{NFSStatusROFS, os.ErrPermission}
	}
	if len(req.Data) > math.MaxInt32 || req.Count > math.MaxInt32 {
		return &NFSStatusError{NFSStatusFBig, os.ErrInvalid}
	}
	if req.How != uint32(unstable) && req.How != uint32(dataSync) && req.How != uint32(fileSync) {
		return &NFSStatusError{NFSStatusInval, os.ErrInvalid}
	}
	Log.Infof("WRITE how=%d offset=%d count=%d", req.How, req.Offset, req.Count)

	// stat first for pre-op wcc.
	fullPath := fs.Join(path...)
	info, err := fs.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &NFSStatusError{NFSStatusNoEnt, err}
		}
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if !info.Mode().IsRegular() {
		return &NFSStatusError{NFSStatusInval, os.ErrInvalid}
	}
	preOpCache := ToFileAttribute(info, fullPath).AsCache()

	// now the actual op.
	file, err := fs.OpenFile(fs.Join(path...), os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return &NFSStatusError{NFSStatusAccess, err}
	}
	if req.Offset > 0 {
		if _, err := file.Seek(int64(req.Offset), io.SeekStart); err != nil {
			return &NFSStatusError{NFSStatusIO, err}
		}
	}
	end := req.Count
	if len(req.Data) < int(end) {
		end = uint32(len(req.Data))
	}
	writtenCount, err := file.Write(req.Data[:end])
	if err != nil {
		Log.Errorf("Error writing: %v", err)
		return &NFSStatusError{statusFromWriteError(err), err}
	}
	// COMPRADOR-PATCH: when the client requests fileSync stability, the
	// expectation is that the WRITE RPC does not return until the bytes
	// are durable on the backing store. The default go-nfs server reports
	// unstable and relies on a follow-up COMMIT RPC — but macOS NFSv3
	// clients do not reliably send COMMIT. Type-assert the file to the
	// DurableSyncer interface (implemented by Comprador's stagingHandle)
	// and block here. This is the bridge's mechanism for making Finder's
	// progress dialog honest about the end-to-end MTP commit duration.
	// See bridge/nfs/write.go for the rationale and trade-offs.
	if req.How == uint32(fileSync) {
		if syncer, ok := file.(DurableSyncer); ok {
			if err := syncer.SyncDurable(); err != nil {
				Log.Errorf("SyncDurable error: %v", err)
				return &NFSStatusError{statusFromWriteError(err), err}
			}
		}
	}
	if err := file.Close(); err != nil {
		Log.Errorf("error closing: %v", err)
		return &NFSStatusError{statusFromWriteError(err), err}
	}

	writer := bytes.NewBuffer([]byte{})
	if err := xdr.Write(writer, uint32(NFSStatusOk)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := WriteWcc(writer, preOpCache, tryStat(fs, path)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, uint32(writtenCount)); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	// Respond with the highest stability we can honestly claim. If the
	// client asked for fileSync and the SyncDurable hook ran above, the
	// bytes are durable on the backing store and we can answer truthfully;
	// otherwise report unstable so the client follows up with a COMMIT
	// RPC (macOS clients in practice rely on idle-flush + fileSync rather
	// than COMMIT; see write.go).
	stabilityReply := unstable
	if req.How == uint32(fileSync) {
		stabilityReply = fileSync
	}
	if err := xdr.Write(writer, stabilityReply); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	if err := xdr.Write(writer, w.Server.ID); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}

	if err := w.Write(writer.Bytes()); err != nil {
		return &NFSStatusError{NFSStatusServerFault, err}
	}
	return nil
}
