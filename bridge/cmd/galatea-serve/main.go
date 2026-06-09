// Command galatea-serve is a minimal standalone harness that serves the
// connected MTP device over Galatea's userspace NFSv4 server
// (github.com/terraceonhigh/galatea) via the mtpfsal FSAL — the same stack the
// production bridge runs under `--nfs`, but without the menu-bar app, the WebDAV
// path, or the resume store. Useful for exercising read/write/mutations against
// real hardware from the command line (see `make galatea-dev` / `galatea-mount`
// / `galatea-umount`): mount via mount_nfs, browse, copy, rename, move.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/terraceonhigh/galatea"

	"comprador/bridge/mtp"
	"comprador/bridge/mtpfsal"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// Probe-bind :0 to learn a free loopback port, then close it and hand the
	// address to galatea.Serve (which does its own net.Listen — no listener
	// injection). Tiny race between close and re-listen; fine for a localhost
	// harness. This is a real interface-flex worth raising with Daedalus: a
	// consumer needs the bound port before serving (main.go prints PORT= for
	// the Swift app). See Galatea Correspondance/04.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("probe-bind failed: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	log.Println("Detecting MTP device...")
	session, err := mtp.NewSessionForLocation(0)
	if err != nil {
		log.Fatalf("MTP session failed: %v", err)
	}
	defer session.Close()
	log.Printf("Connected to: %s", session.DeviceName())

	root, resolver := mtpfsal.Root(session)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stdout, "PORT=%d\n", port)
	fmt.Fprintf(os.Stdout, "DEVICE=%s\n", session.DeviceName())
	os.Stdout.Sync()

	log.Printf("Galatea NFSv4 server on %s", addr)
	log.Printf("Mount:   mkdir -p /tmp/galmnt && mount_nfs -o vers=4.0,port=%d,mountport=%d,tcp localhost:/ /tmp/galmnt", port, port)
	log.Printf("Unmount: umount /tmp/galmnt")

	if err := galatea.Serve(ctx, root, resolver, addr); err != nil {
		log.Fatalf("galatea.Serve: %v", err)
	}
	log.Println("server stopped")
}
