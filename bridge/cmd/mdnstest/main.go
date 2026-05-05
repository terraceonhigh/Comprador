// Standalone smoke-test: exercise mtp.RegisterHostname without an MTP device.
// Verifies that the dns-sd wrapper actually publishes <name>.local → 127.0.0.1.
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"androidfs/bridge/mtp"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s <friendly-device-name>", os.Args[0])
	}
	name := os.Args[1]

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	defer listener.Close()

	reg, err := mtp.RegisterHostname(name, port)
	if err != nil {
		log.Fatalf("RegisterHostname failed: %v", err)
	}
	defer reg.Stop()

	fmt.Printf("Registered %s → 127.0.0.1 on port %d\n", reg.Hostname, port)
	fmt.Printf("Try: dscacheutil -q host -a name %s\n", reg.Hostname)
	fmt.Println("Sleeping 5s, then exiting.")
	time.Sleep(5 * time.Second)
}
