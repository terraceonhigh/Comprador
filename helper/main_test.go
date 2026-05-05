package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestHosts writes initial content to a temp hosts file and points the
// global hostsPath at it. Returns the path so the caller can read it back.
func setupTestHosts(t *testing.T, initial string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hosts")
	if err := os.WriteFile(p, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	hostsPath = p
	return p
}

func readHostsContent(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddHostCreatesBlock(t *testing.T) {
	p := setupTestHosts(t, "127.0.0.1\tlocalhost\n255.255.255.255\tbroadcasthost\n")
	if err := addHost("Pixel-6"); err != nil {
		t.Fatal(err)
	}
	got := readHostsContent(t, p)
	if !strings.Contains(got, beginMark) {
		t.Errorf("missing begin marker:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1\tPixel-6") {
		t.Errorf("missing host line:\n%s", got)
	}
	// Original entries preserved
	if !strings.Contains(got, "localhost") || !strings.Contains(got, "broadcasthost") {
		t.Errorf("clobbered existing entries:\n%s", got)
	}
}

func TestAddHostIsIdempotent(t *testing.T) {
	p := setupTestHosts(t, "127.0.0.1\tlocalhost\n")
	if err := addHost("Pixel-6"); err != nil {
		t.Fatal(err)
	}
	if err := addHost("Pixel-6"); err != nil {
		t.Fatal(err)
	}
	got := readHostsContent(t, p)
	if strings.Count(got, "Pixel-6") != 1 {
		t.Errorf("expected single Pixel-6 entry, got:\n%s", got)
	}
}

func TestAddSecondHostInExistingBlock(t *testing.T) {
	p := setupTestHosts(t, "127.0.0.1\tlocalhost\n")
	if err := addHost("Pixel-6"); err != nil {
		t.Fatal(err)
	}
	if err := addHost("Galaxy-S24"); err != nil {
		t.Fatal(err)
	}
	got := readHostsContent(t, p)
	if !strings.Contains(got, "Pixel-6") || !strings.Contains(got, "Galaxy-S24") {
		t.Errorf("missing entries:\n%s", got)
	}
	// Two BEGIN markers would mean we created a second block.
	if strings.Count(got, beginMark) != 1 {
		t.Errorf("expected single managed block, got:\n%s", got)
	}
}

func TestRemoveHost(t *testing.T) {
	p := setupTestHosts(t, "127.0.0.1\tlocalhost\n")
	addHost("Pixel-6")
	addHost("Galaxy-S24")
	if err := removeHost("Pixel-6"); err != nil {
		t.Fatal(err)
	}
	got := readHostsContent(t, p)
	if strings.Contains(got, "Pixel-6") {
		t.Errorf("Pixel-6 still present:\n%s", got)
	}
	if !strings.Contains(got, "Galaxy-S24") {
		t.Errorf("Galaxy-S24 was clobbered:\n%s", got)
	}
}

func TestRemoveLastHostStripsBlock(t *testing.T) {
	p := setupTestHosts(t, "127.0.0.1\tlocalhost\n")
	addHost("Pixel-6")
	if err := removeHost("Pixel-6"); err != nil {
		t.Fatal(err)
	}
	got := readHostsContent(t, p)
	if strings.Contains(got, beginMark) {
		t.Errorf("empty block was not removed:\n%s", got)
	}
	if !strings.Contains(got, "localhost") {
		t.Errorf("clobbered original entries:\n%s", got)
	}
}

func TestClearRemovesEntireBlock(t *testing.T) {
	p := setupTestHosts(t, "127.0.0.1\tlocalhost\n")
	addHost("Pixel-6")
	addHost("Galaxy-S24")
	if err := clearHosts(); err != nil {
		t.Fatal(err)
	}
	got := readHostsContent(t, p)
	if strings.Contains(got, beginMark) || strings.Contains(got, "Pixel-6") || strings.Contains(got, "Galaxy-S24") {
		t.Errorf("block not cleared:\n%s", got)
	}
	if !strings.Contains(got, "localhost") {
		t.Errorf("clobbered original entries:\n%s", got)
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"Pixel-6", "GalaxyS24", "a", "OnePlus12-Pro"}
	bad := []string{
		"", "google.com", "127.0.0.1", "localhost", "name with space",
		"-leadinghyphen", "1leadingdigit", "name!", "ip6-localhost",
	}
	for _, n := range good {
		if err := validateName(n); err != nil {
			t.Errorf("expected %q to be valid, got %v", n, err)
		}
	}
	for _, n := range bad {
		if err := validateName(n); err == nil {
			t.Errorf("expected %q to be invalid, got nil", n)
		}
	}
}

func TestDispatch(t *testing.T) {
	setupTestHosts(t, "127.0.0.1\tlocalhost\n")
	cases := []struct {
		in, want string
	}{
		{"PING", "OK"},
		{"ADD Pixel-6", "OK"},
		{"ADD Pixel-6", "OK"},        // idempotent
		{"ADD bad.name", "ERR invalid name (must match ^[A-Za-z][A-Za-z0-9-]{0,62}$)"},
		{"ADD ", "ERR missing name"},
		{"REMOVE Pixel-6", "OK"},
		{"BOGUS", "ERR unknown command"},
		{"add Galaxy-S24", "OK"}, // case-insensitive command
	}
	for _, c := range cases {
		got := dispatch(c.in)
		if got != c.want {
			t.Errorf("dispatch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
