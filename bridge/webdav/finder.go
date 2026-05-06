package webdav

import (
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/webdav"
)

// finderProbeFiles are files that Finder probes for on every directory access.
// Return 404 immediately without touching MTP.
var finderProbeFiles = []string{
	".DS_Store",
	"desktop.ini",
	"Thumbs.db",
	".Spotlight-V100",
	".fseventsd",
	".Trashes",
	".metadata_never_index",
	".metadata_never_index_unless_rootfs",
	".metadata_direct_scope_only",
	".hidden",
	".TemporaryItems",
	".apdisk",
	".vol",
	".com.apple.timemachine.donotpresent",
	"DCIM/.Trashes",
}

// isFinderProbe returns true if the path is a Finder metadata probe
// that should be immediately answered with 404.
func isFinderProbe(reqPath string) bool {
	base := path.Base(reqPath)

	// AppleDouble resource fork files
	if strings.HasPrefix(base, "._") {
		return true
	}

	for _, probe := range finderProbeFiles {
		if base == probe {
			return true
		}
	}
	return false
}

// noopLockSystem is a webdav.LockSystem that grants every lock and confirms
// every condition without tracking state. This is the correct behaviour for an
// MTP backend: MTP itself serialises operations on the device, so the WebDAV
// lock layer doesn't need to enforce mutual exclusion. We just need to satisfy
// Finder's protocol expectations.
//
// Why this exists: macOS Finder takes a LOCK before chunked PUT uploads and
// frequently neglects to UNLOCK between chunks. With a real lock manager
// (webdav.NewMemLS), the dangling lock causes subsequent PUT/DELETE/MOVE
// operations to fail with 423 Locked, which the kernel surfaces to the user
// as "Error code -36" and aborts the copy partway through.
type noopLockSystem struct {
	counter atomic.Uint64
}

func (n *noopLockSystem) Confirm(_ time.Time, _, _ string, _ ...webdav.Condition) (func(), error) {
	return func() {}, nil
}

func (n *noopLockSystem) Create(_ time.Time, details webdav.LockDetails) (string, error) {
	id := n.counter.Add(1)
	return fmt.Sprintf("opaquelocktoken:comprador-%d", id), nil
}

func (n *noopLockSystem) Refresh(_ time.Time, _ string, _ time.Duration) (webdav.LockDetails, error) {
	return webdav.LockDetails{Duration: -1}, nil
}

func (n *noopLockSystem) Unlock(_ time.Time, _ string) error {
	return nil
}
