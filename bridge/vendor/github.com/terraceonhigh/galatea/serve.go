// Package galatea is the public entry point to Galatea's userspace NFSv4 server.
//
// Galatea lets a host expose any backend that implements the FSAL interface
// (github.com/terraceonhigh/galatea/pkg/virtual: Directory / Leaf) as a volume
// the macOS kernel NFS client can mount — no kernel extension, no root. Serve
// stands the server up over a backend; the host then mounts it with the stock
// client:
//
//	mount_nfs -o vers=4.0,port=N,mountport=N,tcp localhost:/ <mountpoint>
//
// The server (and the wire protocol) live under internal/, which external
// modules cannot import — this package is the supported public surface. A second
// consumer in another repository (e.g. an NTFS or MTP backend) imports this
// package, supplies its own virtual.Directory + HandleResolver, and calls Serve
// (or ServeListener, when it needs to bind its own listener first to learn the
// port).
package galatea

import (
	"context"
	"fmt"
	"net"

	nfssrv "github.com/terraceonhigh/galatea/internal/nfsv4"
	nfsproto "github.com/terraceonhigh/galatea/internal/xdr/pkg/protocols/nfsv4"
	"github.com/terraceonhigh/galatea/internal/xdr/pkg/rpcserver"
	"github.com/terraceonhigh/galatea/pkg/virtual"
)

// Serve runs Galatea's NFSv4 server over root (with resolver mapping NFSv4 file
// handles back to nodes — see virtual.HandleResolver) on the loopback TCP address
// addr, until ctx is cancelled. It is a convenience wrapper that listens on addr
// and hands the listener to ServeListener; see ServeListener for the read-write
// capability and the cancellation semantics.
//
// A host that needs to learn its bound port before serving (e.g. to advertise it
// to a separate process) should bind its own net.Listener on "127.0.0.1:0", read
// l.Addr(), and call ServeListener directly — that avoids the close-and-relisten
// race of probing the port through Serve.
func Serve(ctx context.Context, root virtual.Directory, resolver virtual.HandleResolver, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("galatea: listen %s: %w", addr, err)
	}
	return ServeListener(ctx, root, resolver, ln)
}

// ServeListener runs Galatea's NFSv4 server over root (with resolver mapping
// NFSv4 file handles back to nodes — see virtual.HandleResolver) on the already-
// bound listener l, until ctx is cancelled. It takes ownership of l and closes it
// before returning. Mirrors net/http's Serve(l net.Listener): bind once, read
// l.Addr() if you need the port, then serve.
//
// It is read-WRITE capable: the backend decides whether mutating operations
// succeed (a read-only FSAL returns ROFS; a writable one carries the writes). The
// underlying program constructor is named NewReadOnlyProgram for historical
// reasons only — writes flow through it (proven against the writable in-memory
// FSAL). So an NTFS or other read-write backend serves correctly here.
//
// ServeListener blocks. On ctx cancellation it closes the listener — which wakes
// the blocking Accept — and returns nil; NFS clients tolerate a server that
// restarts. It returns a non-nil error only on an unexpected accept failure that
// is not a cancellation.
//
// Cancellation semantics, stated precisely (a backend with a single global cursor
// — e.g. an MTP session goroutine — relies on knowing exactly when it is safe to
// release the device): cancelling ctx stops Galatea accepting *new* connections
// and returns. It does NOT interrupt an in-flight request, and ServeListener does
// NOT wait for in-flight connection goroutines before returning — a connection
// whose handler is mid-VirtualRead runs to the end of that bounded operation and
// unwinds only when its peer (the kernel NFS client) closes the TCP connection,
// which a Finder eject/unmount does. So "the backend is safe to release" is true
// once the client has disconnected, not at the instant ServeListener returns. The
// teardown itself is safe: the handler completes its current op, closes its own
// connection exactly once, and never wedges.
func ServeListener(ctx context.Context, root virtual.Directory, resolver virtual.HandleResolver, l net.Listener) error {
	program := nfssrv.NewReadOnlyProgram(root, resolver)
	server := rpcserver.NewServer(
		map[uint32]rpcserver.Service{
			nfsproto.NFS4_PROGRAM_PROGRAM_NUMBER: nfsproto.NewNfs4ProgramService(program),
		},
		nfssrv.NewSystemAuthenticator(),
	)

	defer l.Close()

	// Close the listener on cancellation so the blocking Accept below returns;
	// distinguish that expected wake-up from a genuine accept error.
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			_ = server.HandleConnection(conn, conn)
			_ = conn.Close()
		}()
	}
}
