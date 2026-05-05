// Comprador helper — runs as root, edits /etc/hosts within an
// Comprador-managed block so the bridge can advertise URLs like
// http://Pixel-6:port/ that NetFS will mount as /Volumes/Pixel-6.
//
// The unprivileged main app talks to this daemon over a Unix domain socket.
// The daemon is registered with launchd via SMAppService.daemon, which
// triggers a single one-time admin approval the first time the app runs.
//
// Protocol (line-based, ASCII):
//     ADD <name>      → 127.0.0.1 <name> appended to managed block
//     REMOVE <name>   → matching line removed from managed block
//     CLEAR           → entire managed block removed
//     PING            → liveness check
// Replies: "OK" or "ERR <reason>"
//
// `<name>` must match ^[A-Za-z][A-Za-z0-9-]{0,62}$ — single-label DNS names
// only. No dots → no impersonating google.com or similar.
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

const (
	defaultHostsPath  = "/etc/hosts"
	defaultSocketPath = "/var/run/comprador-helper.sock"
	beginMark         = "# Comprador BEGIN — managed by comprador-helper, do not edit"
	endMark           = "# Comprador END"
)

// Overridable for tests; set via env vars on startup.
var (
	hostsPath  = defaultHostsPath
	socketPath = defaultSocketPath
)

var (
	validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,62}$`)
	mu        sync.Mutex // serialise hosts edits
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	if v := os.Getenv("COMPRADOR_HOSTS_PATH"); v != "" {
		hostsPath = v
	}
	if v := os.Getenv("COMPRADOR_SOCKET_PATH"); v != "" {
		socketPath = v
	}
	requireRoot := os.Getenv("COMPRADOR_SKIP_ROOT_CHECK") == ""

	log.Printf("comprador-helper starting (pid %d, hosts=%s, sock=%s)",
		os.Getpid(), hostsPath, socketPath)

	if requireRoot && os.Geteuid() != 0 {
		log.Fatalf("must run as root (got euid %d)", os.Geteuid())
	}

	// Remove stale socket from a previous run.
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	// World-writable socket so the unprivileged GUI app can connect.
	// Authorisation comes from strict input validation below, not socket
	// perms — anything that gets through ADD is a single-label hostname
	// pointing at 127.0.0.1, which can't impersonate real domains.
	if err := os.Chmod(socketPath, 0o666); err != nil {
		log.Printf("chmod socket: %v", err)
	}

	// Clean up socket on SIGINT/SIGTERM.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigs
		log.Printf("received %s, shutting down", s)
		listener.Close()
		_ = os.Remove(socketPath)
		os.Exit(0)
	}()

	log.Printf("listening on %s", socketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			return
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		reply := dispatch(line)
		if _, err := io.WriteString(conn, reply+"\n"); err != nil {
			return
		}
	}
}

func dispatch(line string) string {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	arg := ""
	if len(parts) == 2 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "PING":
		return "OK"
	case "ADD":
		if err := validateName(arg); err != nil {
			return "ERR " + err.Error()
		}
		if err := withLock(func() error { return addHost(arg) }); err != nil {
			return "ERR " + err.Error()
		}
		return "OK"
	case "REMOVE":
		if err := validateName(arg); err != nil {
			return "ERR " + err.Error()
		}
		if err := withLock(func() error { return removeHost(arg) }); err != nil {
			return "ERR " + err.Error()
		}
		return "OK"
	case "CLEAR":
		if err := withLock(clearHosts); err != nil {
			return "ERR " + err.Error()
		}
		return "OK"
	default:
		return "ERR unknown command"
	}
}

func withLock(fn func() error) error {
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("missing name")
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid name (must match ^[A-Za-z][A-Za-z0-9-]{0,62}$)")
	}
	// Reject reserved labels that would be confusing or actively harmful.
	low := strings.ToLower(name)
	switch low {
	case "localhost", "broadcasthost", "local", "ip6-localhost", "ip6-loopback":
		return fmt.Errorf("name reserved")
	}
	return nil
}

// addHost ensures 127.0.0.1 <name> exists exactly once in the managed block.
func addHost(name string) error {
	lines, err := readHosts()
	if err != nil {
		return err
	}
	begin, end := findBlock(lines)
	target := "127.0.0.1\t" + name

	if begin == -1 {
		// No block yet — append one.
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, beginMark, target, endMark)
		log.Printf("created managed block, added %s", name)
		return writeHosts(lines)
	}

	// Block exists — check for duplicate.
	for i := begin + 1; i < end; i++ {
		if matchesHost(lines[i], name) {
			return nil // already present
		}
	}
	// Insert before the END marker.
	updated := append([]string{}, lines[:end]...)
	updated = append(updated, target)
	updated = append(updated, lines[end:]...)
	log.Printf("added %s", name)
	return writeHosts(updated)
}

// removeHost drops any 127.0.0.1 <name> lines from the managed block.
// If the block is now empty, the markers are removed too.
func removeHost(name string) error {
	lines, err := readHosts()
	if err != nil {
		return err
	}
	begin, end := findBlock(lines)
	if begin == -1 {
		return nil
	}

	var kept []string
	kept = append(kept, lines[:begin+1]...)
	for i := begin + 1; i < end; i++ {
		if !matchesHost(lines[i], name) {
			kept = append(kept, lines[i])
		}
	}
	kept = append(kept, lines[end:]...)

	// If the block is empty, strip the markers (and a single preceding blank).
	begin2, end2 := findBlock(kept)
	if begin2 != -1 && end2-begin2 == 1 {
		dropFrom := begin2
		if dropFrom > 0 && kept[dropFrom-1] == "" {
			dropFrom--
		}
		kept = append(kept[:dropFrom], kept[end2+1:]...)
	}
	log.Printf("removed %s", name)
	return writeHosts(kept)
}

func clearHosts() error {
	lines, err := readHosts()
	if err != nil {
		return err
	}
	begin, end := findBlock(lines)
	if begin == -1 {
		return nil
	}
	dropFrom := begin
	if dropFrom > 0 && lines[dropFrom-1] == "" {
		dropFrom--
	}
	cleared := append(lines[:dropFrom], lines[end+1:]...)
	log.Printf("cleared managed block")
	return writeHosts(cleared)
}

func findBlock(lines []string) (begin, end int) {
	begin, end = -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == beginMark || strings.HasPrefix(t, "# Comprador BEGIN") {
			begin = i
		}
		if t == endMark && begin != -1 {
			end = i
			return
		}
	}
	return
}

func matchesHost(line, name string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "127.0.0.1" {
		return false
	}
	for _, f := range fields[1:] {
		if strings.EqualFold(f, name) {
			return true
		}
	}
	return false
}

func readHosts() ([]string, error) {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", hostsPath, err)
	}
	// Preserve trailing newline behaviour by splitting without trimming.
	s := string(data)
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// writeHosts writes via tempfile + rename for atomicity.
func writeHosts(lines []string) error {
	out := strings.Join(lines, "\n") + "\n"
	dir := filepath.Dir(hostsPath)
	tmp, err := os.CreateTemp(dir, ".comprador-hosts-*")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpName, hostsPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
