package mtp

/*
#include <string.h>
#include <stdint.h>

// Return codes for MTP data handlers.
#define LIBMTP_HANDLER_RETURN_OK 0
#define LIBMTP_HANDLER_RETURN_ERROR 1
#define LIBMTP_HANDLER_RETURN_CANCEL 2
*/
import "C"
import (
	"io"
	"unsafe"
)

// goDataPutFunc is called by libmtp when streaming file data FROM the device (download).
// Signature matches MTPDataPutFunc:
//
//	uint16_t (void* params, void* priv, uint32_t sendlen, unsigned char *data, uint32_t *putlen)
//
// params: opaque libmtp params (unused by us)
// priv: our callback_ctx with the registry ID
// sendlen: number of bytes available in data
// data: the file data from the device
// putlen: out — number of bytes we consumed
//
//export goDataPutFunc
func goDataPutFunc(params unsafe.Pointer, priv unsafe.Pointer, sendlen C.uint32_t, data *C.uchar, putlen *C.uint32_t) C.uint16_t {
	ctx := (*struct{ id C.int })(priv)
	callbackRegistry.mu.Lock()
	entry, ok := callbackRegistry.writers[int(ctx.id)]
	callbackRegistry.mu.Unlock()
	if !ok {
		return C.LIBMTP_HANDLER_RETURN_ERROR
	}

	// Reuse entry.buf instead of C.GoBytes (which allocates fresh).
	// Grow the buffer once if libmtp's chunk exceeds our initial size;
	// subsequent callbacks reuse the new capacity.
	n := int(sendlen)
	if cap(entry.buf) < n {
		entry.buf = make([]byte, n)
	}
	goSlice := entry.buf[:n]
	C.memcpy(unsafe.Pointer(&goSlice[0]), unsafe.Pointer(data), C.ulong(n))
	written, err := entry.w.Write(goSlice)
	*putlen = C.uint32_t(written)
	if err != nil {
		return C.LIBMTP_HANDLER_RETURN_ERROR
	}
	return C.LIBMTP_HANDLER_RETURN_OK
}

// goDataGetFunc is called by libmtp when streaming file data TO the device (upload).
// Signature matches MTPDataGetFunc:
//
//	uint16_t (void* params, void* priv, uint32_t wantlen, unsigned char *data, uint32_t *gotlen)
//
// params: opaque libmtp params (unused by us)
// priv: our callback_ctx with the registry ID
// wantlen: how many bytes libmtp wants
// data: buffer to fill with data for the device
// gotlen: out — how many bytes we provided
//
//export goDataGetFunc
func goDataGetFunc(params unsafe.Pointer, priv unsafe.Pointer, wantlen C.uint32_t, data *C.uchar, gotlen *C.uint32_t) C.uint16_t {
	ctx := (*struct{ id C.int })(priv)
	callbackRegistry.mu.Lock()
	entry, ok := callbackRegistry.readers[int(ctx.id)]
	callbackRegistry.mu.Unlock()
	if !ok {
		return C.LIBMTP_HANDLER_RETURN_ERROR
	}

	// Reuse entry.buf instead of `make([]byte, int(wantlen))` per call.
	// Grow once if a libmtp request exceeds our initial size; subsequent
	// callbacks reuse the new capacity. This is the path #1 fix from
	// docs/DECISIONS.md — caps Go-side memory at one chunk for the
	// whole transfer, instead of accumulating one allocation per chunk.
	want := int(wantlen)
	if cap(entry.buf) < want {
		entry.buf = make([]byte, want)
	}
	goSlice := entry.buf[:want]
	n, err := entry.r.Read(goSlice)
	if n > 0 {
		C.memcpy(unsafe.Pointer(data), unsafe.Pointer(&goSlice[0]), C.ulong(n))
	}
	*gotlen = C.uint32_t(n)
	// io.EOF signals end of data — return OK with gotlen=0 so libmtp knows we're done.
	// Only return ERROR for real errors (not EOF).
	if err != nil && err != io.EOF && n == 0 {
		return C.LIBMTP_HANDLER_RETURN_ERROR
	}
	return C.LIBMTP_HANDLER_RETURN_OK
}
