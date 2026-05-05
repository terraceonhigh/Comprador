package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"androidfs/bridge/mtp"
	"androidfs/bridge/webdav"
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

	// Register a per-device .local hostname → 127.0.0.1 via mDNS so that
	// NetFS, which auto-names volumes after the URL host, gives us a
	// Finder volume named after the device instead of "127.0.0.1".
	host := "127.0.0.1"
	hostReg, err := mtp.RegisterHostname(deviceName, port)
	if err != nil {
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
