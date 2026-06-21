package main

import (
	"context"
	"flag"
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

// BuildID is overridden at link time via -ldflags "-X main.BuildID=...".
// Defaults to "dev" so a bare `go build` still works.
var BuildID = "dev"

func main() {
	// --nfs is now a no-op: Galatea NFSv4 is the only serving mode (WebDAV was
	// retired in v0.4.0). The flag is still accepted because the menu-bar app
	// passes it; remove it from the app side before dropping it here.
	_ = flag.Bool("nfs", true, "deprecated, ignored — NFSv4 is the only mode")
	// --device-loc-id selects which physical MTP device this bridge
	// instance claims, by macOS IOKit USB Location ID. Required when
	// multiple MTP devices are plugged in; if 0/absent the first
	// detected device is opened (single-device behavior).
	// Accepts decimal or hex (0x...) per flag.Uint64 conventions.
	deviceLocID := flag.Uint64("device-loc-id", 0, "macOS IOKit USB Location ID of the target MTP device; 0 = first detected")
	flag.Parse()

	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("bridge build: %s", BuildID)
	if *deviceLocID != 0 {
		log.Printf("Targeting device locationID=0x%08x", uint32(*deviceLocID))
	}

	// Bind to a random localhost port first, before device detection.
	// This lets us fail fast on port issues.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	log.Println("Detecting MTP device...")
	session, err := mtp.NewSessionForLocation(uint32(*deviceLocID))
	if err != nil {
		log.Fatalf("MTP session failed: %v", err)
	}
	defer session.Close()

	deviceName := session.DeviceName()
	log.Printf("Connected to: %s", deviceName)

	// Catch SIGINT/SIGTERM so we run our cleanup defers instead of dying
	// instantly. ctx is cancelled on signal; ServeListener closes the listener
	// on cancel.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Register a per-device .local hostname that resolves to 127.0.0.1 via
	// mDNS. NetFS / mount(8) display the source's hostname in Finder's
	// Locations sidebar, so mounting via `XQ-BT52.local:/` gets the user a
	// sidebar entry named "XQ-BT52.local" (or "XQ-BT52" — macOS strips the
	// suffix in some views) instead of the bare "localhost".
	host := "localhost"
	if hostReg, err := mtp.RegisterHostname(deviceName, port); err != nil {
		log.Printf("Hostname registration failed (mount source will be 'localhost'): %v", err)
	} else {
		host = hostReg.Hostname
		defer hostReg.Stop()
	}

	fmt.Fprintf(os.Stdout, "PORT=%d\n", port)
	fmt.Fprintf(os.Stdout, "HOST=%s\n", host)
	fmt.Fprintf(os.Stdout, "PROTO=nfs\n")
	fmt.Fprintf(os.Stdout, "DEVICE=%s\n", deviceName)
	os.Stdout.Sync()

	// Galatea: in-house userspace NFSv4 server over the MTP FSAL
	// (bridge/mtpfsal) — the only serving mode (WebDAV and the willscott/go-nfs
	// NFSv3 path are both retired). The listener is already bound (port printed
	// above); ServeListener takes ownership and closes it on ctx cancellation —
	// no probe-bind, no double-close. Clients mount with vers=4.0 (the Swift
	// app's mountNFS uses that).
	root, resolver := mtpfsal.Root(session)
	log.Printf("Galatea NFSv4 server listening on 127.0.0.1:%d (mDNS host: %s)", port, host)
	log.Printf("Mount command:")
	log.Printf("  mkdir -p /tmp/comprador")
	log.Printf("  mount -t nfs -o vers=4.0,port=%d,mountport=%d,tcp %s:/ /tmp/comprador", port, port, host)
	log.Printf("Unmount: umount /tmp/comprador")

	if err := galatea.ServeListener(ctx, root, resolver, listener); err != nil {
		log.Printf("NFS server stopped: %v", err)
	}
}
