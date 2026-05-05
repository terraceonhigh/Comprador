package mtp

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// HostnameRegistration runs `dns-sd -P` as a child process to publish a
// custom <hostname>.local A record pointing at 127.0.0.1. NetFS's WebDAV
// mount uses the URL hostname as the volume name in /Volumes, so this is
// how we get "Pixel-6" in Finder instead of "127.0.0.1".
type HostnameRegistration struct {
	Hostname string // e.g. "Pixel-6.local"
	cmd      *exec.Cmd
	once     sync.Once
}

// dnsSDServicePrefix tags our dns-sd subprocesses so we can detect orphans
// from a previous crashed bridge run.
const dnsSDServicePrefix = "Comprador-"

// RegisterHostname publishes <hostname>.local → 127.0.0.1 via mDNS by
// spawning `dns-sd -P`. Blocks until the registration is confirmed (or
// timeout). Caller must call Stop() to terminate the dns-sd child.
//
// `friendlyName` is the device's friendly name (e.g. "Pixel 6"); it gets
// sanitised into a DNS-safe label.
func RegisterHostname(friendlyName string, port int) (*HostnameRegistration, error) {
	// Reap any dns-sd left over from a previous bridge that didn't shut
	// down cleanly. Otherwise the new registration's hostname could still
	// be answered by the orphan, or service names collide.
	killOrphanDNSSD()

	label := sanitizeHostnameLabel(friendlyName)
	if label == "" {
		label = "android"
	}
	host := label + ".local"

	// dns-sd -P <Service Name> <Type> <Domain> <Port> <Host> <IPaddress>
	// Service name is irrelevant for our purposes but must be unique-ish.
	cmd := exec.Command("/usr/bin/dns-sd",
		"-P", dnsSDServicePrefix+label,
		"_http._tcp", "local",
		fmt.Sprintf("%d", port),
		host,
		"127.0.0.1",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("dns-sd stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("dns-sd start: %w", err)
	}

	// Wait for "Name now registered and active" for the host record.
	confirmed := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("dns-sd: %s", line)
			if strings.Contains(line, host) && strings.Contains(line, "registered and active") {
				confirmed <- nil
				// Keep draining stdout so the pipe doesn't block.
				go io.Copy(io.Discard, stdout)
				return
			}
		}
		confirmed <- fmt.Errorf("dns-sd exited before confirming registration")
	}()

	select {
	case err := <-confirmed:
		if err != nil {
			cmd.Process.Kill()
			return nil, err
		}
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		return nil, fmt.Errorf("dns-sd registration timed out")
	}

	log.Printf("Registered mDNS hostname: %s → 127.0.0.1", host)
	return &HostnameRegistration{Hostname: host, cmd: cmd}, nil
}

// Stop terminates the dns-sd subprocess and removes the mDNS registration.
func (h *HostnameRegistration) Stop() {
	h.once.Do(func() {
		if h.cmd != nil && h.cmd.Process != nil {
			h.cmd.Process.Kill()
			h.cmd.Wait()
		}
	})
}

// killOrphanDNSSD sends SIGKILL to any leftover `dns-sd -P Comprador-*`
// processes from a prior bridge run that didn't shut down cleanly.
func killOrphanDNSSD() {
	cmd := exec.Command("/usr/bin/pkill", "-f", "dns-sd.*"+dnsSDServicePrefix)
	_ = cmd.Run() // exit status 1 just means no matches; ignore.
}

var dnsLabelInvalid = regexp.MustCompile(`[^A-Za-z0-9-]+`)

// sanitizeHostnameLabel converts a device friendly name into a valid DNS
// label per RFC 1035: letters, digits, hyphens; <=63 chars; must not begin
// or end with a hyphen.
func sanitizeHostnameLabel(name string) string {
	// Replace common separators with hyphens.
	s := strings.NewReplacer(" ", "-", "_", "-", ".", "-").Replace(name)
	s = dnsLabelInvalid.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
		s = strings.TrimRight(s, "-")
	}
	return s
}
