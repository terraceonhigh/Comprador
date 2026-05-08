// Standalone smoke-test: prove macOS can mount a go-nfs server on loopback
// without the ~90s wait that WebDAVFS triggers on quota properties.
//
// Phase 1 verification procedure:
//
//  1. Build: make nfs-stub
//  2. Run:   ./build/nfsstub
//     (note the port printed to stdout, e.g. PORT=54321)
//  3. In a second terminal (requires sudo):
//     mkdir -p /tmp/nfsstub
//     sudo mount -o port=<N>,mountport=<N>,nfsvers=3,nolocks,tcp -t nfs localhost:/ /tmp/nfsstub
//  4. Expected: mount returns quickly (<5s). Open Finder — volume appears.
//     The files hello.txt and Photos/readme.txt should be visible.
//  5. Unmount: sudo umount /tmp/nfsstub
//
// If the mount returns quickly and Finder shows the files, Phase 1 is verified
// and the NFS pivot is safe to proceed.
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
	"github.com/willscott/go-nfs/helpers/memfs"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	fs := memfs.New()

	// Populate with a small test tree so Finder has something to show.
	if f, err := fs.Create("hello.txt"); err == nil {
		f.Write([]byte("Hello from Comprador NFS stub — Phase 1 verification\n"))
		f.Close()
	}
	if err := fs.MkdirAll("Photos", 0755); err == nil {
		if f, err := fs.Create("Photos/readme.txt"); err == nil {
			f.Write([]byte("This would be your phone's Photos folder.\n"))
			f.Close()
		}
	}

	handler := nfshelper.NewNullAuthHandler(fs)
	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)

	fmt.Fprintf(os.Stdout, "PORT=%d\n", port)
	os.Stdout.Sync()

	log.Printf("NFS stub listening on 127.0.0.1:%d", port)
	log.Printf("Mount command (run in a separate terminal):")
	log.Printf("  mkdir -p /tmp/nfsstub")
	log.Printf("  sudo mount -o port=%d,mountport=%d,nfsvers=3,nolocks,tcp -t nfs localhost:/ /tmp/nfsstub", port, port)
	log.Printf("Unmount:")
	log.Printf("  sudo umount /tmp/nfsstub")

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigs
		log.Printf("Received %s, shutting down", s)
		listener.Close()
	}()

	if err := nfs.Serve(listener, cacheHelper); err != nil {
		log.Printf("NFS server stopped: %v", err)
	}
}
