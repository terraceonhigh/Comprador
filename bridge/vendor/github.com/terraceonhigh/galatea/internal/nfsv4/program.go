package nfsv4

import (
	"time"

	"github.com/terraceonhigh/galatea/internal/bb/clock"
	"github.com/terraceonhigh/galatea/internal/bb/filesystem/path"
	"github.com/terraceonhigh/galatea/internal/bb/random"
	"github.com/terraceonhigh/galatea/internal/xdr/pkg/protocols/nfsv4"
	"github.com/terraceonhigh/galatea/pkg/virtual"
)

// NewReadOnlyProgram builds an NFSv4.0 server program serving the FSAL backend
// rooted at root, with defaults suitable for a single-user localhost mount. It
// is Galatea's convenience constructor over bb-rex's NewNFS40Program, filling
// the parameters a host would otherwise have to supply.
//
// resolver maps an opaque file handle back to its node (DEC-017 Option B); the
// server needs it whenever a client replays a handle via PUTFH (which the macOS
// client does constantly — GETFH to cache, PUTFH to return). For the in-memory
// FSAL, pass virtual.NewMemoryHandleResolver(root). The root must also provide a
// FileHandle attribute (the in-memory FSAL does), or NewNFS40Program panics.
func NewReadOnlyProgram(root virtual.Directory, resolver virtual.HandleResolver) nfsv4.Nfs4Program {
	var verifier nfsv4.Verifier4
	var stateIDOtherPrefix [stateIDOtherPrefixLength]byte
	return NewNFS40Program(
		root,
		NewOpenedFilesPool(resolver),
		random.NewFastSingleThreadedGenerator(),
		verifier,
		stateIDOtherPrefix,
		clock.SystemClock,
		time.Minute, // enforced lease time
		time.Minute, // announced lease time
		path.UNIXFormat,
	)
}
