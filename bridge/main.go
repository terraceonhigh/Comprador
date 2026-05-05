package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"comprador/bridge/mtp"
	"comprador/bridge/webdav"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// Bind to a random localhost port first, before device detection.
	// This lets us fail fast on port issues.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	log.Println("Detecting MTP device...")
	session, err := mtp.NewSession()
	if err != nil {
		log.Fatalf("MTP session failed: %v", err)
	}
	defer session.Close()

	deviceName := session.DeviceName()
	log.Printf("Connected to: %s", deviceName)

	handler := webdav.NewHandler(session)

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

	// Catch SIGINT/SIGTERM so we run our cleanup defers (dns-sd subprocess,
	// MTP session) instead of dying instantly. Go's runtime skips defers
	// on default signal handling.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigs
		log.Printf("Received %s, shutting down", s)
		listener.Close()
	}()

	if err := http.Serve(listener, handler); err != nil {
		log.Printf("HTTP server stopped: %v", err)
	}
}
