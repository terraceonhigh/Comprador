package virtual

import (
	"context"
	"math/bits"
)

// ShareMask is a bitmask of operations permitted against an opened Leaf.
// Transcribed from bb-rex's virtual.ShareMask.
type ShareMask uint32

const (
	// ShareMaskRead permits calls to VirtualRead().
	ShareMaskRead ShareMask = 1 << iota
	// ShareMaskWrite permits calls to VirtualWrite().
	ShareMaskWrite
)

// Count returns the number of permitted operations.
func (sm ShareMask) Count() uint {
	return uint(bits.OnesCount32(uint32(sm)))
}

// OpenExistingOptions describes what should happen to a file when opened.
// Truncate corresponds to open()'s O_TRUNC; it has no effect on freshly
// created files, which are always empty.
type OpenExistingOptions struct {
	Truncate bool
}

// ToAttributesMask converts open options to an AttributesMask indicating
// which attributes the operation affected.
func (o *OpenExistingOptions) ToAttributesMask() (m AttributesMask) {
	if o.Truncate {
		m |= AttributesMaskSizeBytes
	}
	return m
}

// Leaf is a node exposed through NFSv4 (or FUSE) that is not a directory:
// a regular file, socket, FIFO, symbolic link or device. Transcribed
// from bb-rex's virtual.Leaf, with filesystem.RegionType replaced by the
// Galatea-native RegionType.
type Leaf interface {
	Node

	VirtualAllocate(off, size uint64) Status
	VirtualSeek(offset uint64, regionType RegionType) (*uint64, Status)
	VirtualOpenSelf(ctx context.Context, shareAccess ShareMask, options *OpenExistingOptions, requested AttributesMask, attributes *Attributes) Status

	// VirtualRead reads into buf starting at offset, returning the number of
	// bytes read, whether end-of-file was reached, and a Status.
	//
	// CONTRACT — bounded per-operation reads (load-bearing; do not optimise
	// away). The server issues exactly one VirtualRead per NFSv4 READ operation;
	// len(buf) is the client's requested count (in practice bounded by its
	// negotiated rsize — the server passes the count through, it does not cap it).
	// It does NOT hold
	// the leaf open across a whole transfer, coalesce a file into one giant
	// VirtualRead, or read ahead. A backend may therefore serialise every
	// device-touching call onto a single cursor (e.g. an MTP session goroutine)
	// without a multi-MB read starving other operations: because each READ is its
	// own short call, the cursor interleaves chunks between concurrent transfers.
	// This non-starvation property is what lets a globally-serialised backend
	// compose with the server's per-open-owner sequencing. A future readahead or
	// whole-file pin would silently reintroduce head-of-line blocking for such
	// backends, so it must remain opt-in and must never become the contract.
	VirtualRead(buf []byte, offset uint64) (n int, eof bool, s Status)

	VirtualClose(shareAccess ShareMask)
	VirtualWrite(buf []byte, offset uint64) (int, Status)
}

// StatelessLeafLinkCount is the link count to report for leaf nodes that
// don't track an explicit one (e.g. CAS-backed files). The kernel stores
// this in i_nlink and may decrement it; a high constant keeps it from
// reaching zero and the file from appearing unlinked. Transcribed from
// bb-rex.
const StatelessLeafLinkCount = 9999

// BoundReadToFileSize helps implementations of VirtualRead() limit a read
// to the actual file size. It returns the truncated buffer and whether
// the read reaches end-of-file. Transcribed from bb-rex.
func BoundReadToFileSize(buf []byte, offset, size uint64) ([]byte, bool) {
	if offset >= size {
		// Read starting at or past end-of-file.
		return nil, true
	}
	if remaining := size - offset; uint64(len(buf)) >= remaining {
		// Read ending at or past end-of-file.
		return buf[:remaining], true
	}
	return buf, false
}
