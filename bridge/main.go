package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/terraceonhigh/galatea"

	"comprador/bridge/mtp"
	"comprador/bridge/mtpfsal"
	"comprador/bridge/resume"
	"comprador/bridge/webdav"
)

// BuildID is overridden at link time via -ldflags "-X main.BuildID=...".
// Defaults to "dev" so a bare `go build` still works.
var BuildID = "dev"

func main() {
	useNFS := flag.Bool("nfs", false, "serve NFSv3 instead of WebDAV")
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
	// instantly. ctx is cancelled on signal; the Galatea path passes it to
	// ServeListener (which closes the listener on cancel), and the WebDAV path
	// closes the listener from ctx.Done below.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *useNFS {
		// Register a per-device .local hostname that resolves to
		// 127.0.0.1 via mDNS. NetFS / mount(8) display the source's
		// hostname in Finder's Locations sidebar, so mounting via
		// `XQ-BT52.local:/` gets the user a sidebar entry named
		// "XQ-BT52.local" (or "XQ-BT52" — macOS strips the suffix in
		// some views) instead of the bare "localhost".
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
		// (bridge/mtpfsal), replacing the patched willscott/go-nfs NFSv3 path
		// and its JUKEBOX/prefetch workaround. The listener is already bound
		// (port printed above); ServeListener takes ownership and closes it on
		// ctx cancellation — no probe-bind, no double-close. Clients mount with
		// vers=4.0 (the Swift app's mountNFS uses that). The willscott server in
		// bridge/nfs remains in the tree (unimported here) for revert until
		// writes are proven and JUKEBOX is deleted.
		root, resolver := mtpfsal.Root(session)
		log.Printf("Galatea NFSv4 server listening on 127.0.0.1:%d (mDNS host: %s)", port, host)
		log.Printf("Mount command:")
		log.Printf("  mkdir -p /tmp/comprador")
		log.Printf("  mount -t nfs -o vers=4.0,port=%d,mountport=%d,tcp %s:/ /tmp/comprador", port, port, host)
		log.Printf("Unmount: umount /tmp/comprador")

		if err := galatea.ServeListener(ctx, root, resolver, listener); err != nil {
			log.Printf("NFS server stopped: %v", err)
		}
		return
	}

	// WebDAV path (default).

	// Resumable-upload store. Persists truncated chunked-PUT bodies
	// (Apple WebDAVFS writeseq cap) under
	// $HOME/Library/Application Support/Comprador/pending/, so the
	// Comprador menu-bar app can drive a side-channel completion. If
	// the store can't be initialised (rare — only fails on permission
	// or disk issues), we log and continue with resumable uploads
	// disabled; the bridge falls back to refusing truncated uploads.
	store, err := resume.NewStore()
	if err != nil {
		log.Printf("Resumable-upload store init failed: %v (truncated uploads will surface as -36)", err)
		store = nil
	} else {
		log.Printf("Resumable-upload store ready (%d pending session(s) recovered)", len(store.List()))
	}

	handler := webdav.NewHandler(session, store)

	// Hostname for the WebDAV URL. NetFS auto-names volumes from the URL
	// host, so a clean, single-label hostname → clean Finder volume name.
	//
	// Resolution order:
	//   1. COMPRADOR_HOST env var, if set (production: provided by the
	//      menu-bar app after the privileged helper has added an entry to
	//      /etc/hosts pointing it at 127.0.0.1).
	//   2. mDNS fallback: register <DeviceName>.local → 127.0.0.1 via
	//      dns-sd. Works without the helper but yields a `.local` suffix.
	//   3. Bare 127.0.0.1, with the corresponding ugly Finder volume name.
	host := "127.0.0.1"
	if envHost := os.Getenv("COMPRADOR_HOST"); envHost != "" {
		host = envHost
		log.Printf("Using app-provided hostname: %s", host)
	} else if hostReg, err := mtp.RegisterHostname(deviceName, port); err != nil {
		log.Printf("Hostname registration failed (volume will be named '127.0.0.1'): %v", err)
	} else {
		host = hostReg.Hostname
		defer hostReg.Stop()
	}

	// Print port and host in structured format for the Swift app to read from stdout.
	fmt.Fprintf(os.Stdout, "PORT=%d\n", port)
	fmt.Fprintf(os.Stdout, "HOST=%s\n", host)
	fmt.Fprintf(os.Stdout, "DEVICE=%s\n", deviceName)
	os.Stdout.Sync()

	log.Printf("WebDAV server listening on http://%s:%d/", host, port)
	log.Printf("Mount with: Finder → Go → Connect to Server → dav://%s:%d/", host, port)

	// Stop http.Serve on SIGINT/SIGTERM by closing the listener.
	go func() {
		<-ctx.Done()
		log.Println("Received signal, shutting down")
		listener.Close()
	}()

	if err := http.Serve(listener, handler); err != nil {
		log.Printf("HTTP server stopped: %v", err)
	}
}
