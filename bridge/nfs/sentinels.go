package nfs

import (
	"io"
	"os"
	"time"

	billy "github.com/go-git/go-billy/v5"
)

// Synthetic sentinel files we serve from the NFS root to influence macOS-side
// behavior. None of these touch the MTP device — they are virtualized entirely
// inside the bridge so that adding or removing a sentinel never pollutes the
// user's phone storage.
//
// See docs/MISTAKES.md §NFS pivot entry 4 for the empirical receipt that
// motivated this: macOS Spotlight, on Finder's first entry into the mounted
// volume, issues a parallel READ probe against every file in the directory
// to extract preview content. Because our READ handler synchronously
// downloads the entire MTP file before responding (libmtp has no
// random-access read), any file larger than what fits in macOS's NFS RPC
// timeout window (~20–30 s, ~600 MB at typical USB-MTP speed) produces an
// unanswered READ. macOS surfaces this to the user as "Server connections
// interrupted: comprador" — Comprador's single most damaging UX defect.
//
// `.metadata_never_index` is the documented macOS-wide mechanism for opting
// a volume out of Spotlight indexing. When present at the volume root,
// `mds` skips the mount entirely: no thumbnail generation, no content
// extraction, no preview reads. The user's drag-drop scenario goes from
// "scary alert + 5-minute stall" to "instant browse."

// sentinelContent maps each synthetic path (clean form, leading slash) to
// the bytes we serve when something reads it. Empty bytes are fine for
// `.metadata_never_index` — macOS only checks the file's existence, not
// its contents.
var sentinelContent = map[string][]byte{
	"/.metadata_never_index": nil,
}

// sentinelInfo reports whether p is a synthetic sentinel path and, if so,
// returns the canonical bytes.
func sentinelInfo(p string) ([]byte, bool) {
	b, ok := sentinelContent[p]
	return b, ok
}

// sentinelFileInfo implements os.FileInfo for a virtualized sentinel.
type sentinelFileInfo struct {
	name string
	size int64
}

func (s sentinelFileInfo) Name() string       { return s.name }
func (s sentinelFileInfo) Size() int64        { return s.size }
func (s sentinelFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (s sentinelFileInfo) IsDir() bool        { return false }
func (s sentinelFileInfo) Sys() interface{}   { return nil }
func (s sentinelFileInfo) Mode() os.FileMode  { return 0644 }

// sentinelHandle implements billy.File over an in-memory byte slice.
// Read-only; writes return billy.ErrReadOnly so we cannot accidentally
// mutate a sentinel via NFS WRITE.
type sentinelHandle struct {
	name string
	data []byte
	pos  int64
}

func (h *sentinelHandle) Name() string { return h.name }

func (h *sentinelHandle) Read(p []byte) (int, error) {
	if h.pos >= int64(len(h.data)) {
		return 0, io.EOF
	}
	n := copy(p, h.data[h.pos:])
	h.pos += int64(n)
	return n, nil
}

func (h *sentinelHandle) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(h.data)) {
		return 0, io.EOF
	}
	n := copy(p, h.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (h *sentinelHandle) Write(_ []byte) (int, error) { return 0, billy.ErrReadOnly }
func (h *sentinelHandle) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = h.pos + offset
	case io.SeekEnd:
		abs = int64(len(h.data)) + offset
	}
	if abs < 0 {
		abs = 0
	}
	h.pos = abs
	return abs, nil
}

func (h *sentinelHandle) Truncate(_ int64) error { return billy.ErrReadOnly }
func (h *sentinelHandle) Lock() error            { return nil }
func (h *sentinelHandle) Unlock() error          { return nil }
func (h *sentinelHandle) Close() error           { return nil }
