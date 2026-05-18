package mtp

import (
	"testing"
	"time"
)

// dirMeta is a convenience helper for tests: builds an ObjectMeta for a
// directory. id is the MTP object ID (must be unique within the test).
func dirMeta(id uint32, path string) *ObjectMeta {
	return &ObjectMeta{
		ID:        id,
		StorageID: 1,
		Name:      path,
		Path:      path,
		IsDir:     true,
		ModTime:   time.Now(),
	}
}

// fileMeta is the file counterpart to dirMeta. parent must be the id of an
// already-inserted directory.
func fileMeta(id uint32, parent uint32, path string) *ObjectMeta {
	return &ObjectMeta{
		ID:        id,
		ParentID:  parent,
		StorageID: 1,
		Name:      path,
		Path:      path,
		IsDir:     false,
		ModTime:   time.Now(),
	}
}

func TestObjectMapBasic(t *testing.T) {
	m := NewObjectMap()
	m.Put(dirMeta(1, "/storage"))
	m.Put(fileMeta(2, 1, "/storage/foo.txt"))

	if got, ok := m.GetByPath("/storage/foo.txt"); !ok || got.ID != 2 {
		t.Fatalf("expected /storage/foo.txt ID=2, got %v ok=%v", got, ok)
	}
	if got, ok := m.GetByID(2); !ok || got.Path != "/storage/foo.txt" {
		t.Fatalf("byID lookup: got %v ok=%v", got, ok)
	}
}

// TestIsFreshTTL verifies the TTL-based staleness check that powers
// phone-side mutation reflection (V0.3.3 item #1). A freshly populated
// directory is fresh; one populated > directoryTTL ago is not.
func TestIsFreshTTL(t *testing.T) {
	m := NewObjectMap()
	const path = "/storage"
	m.Put(dirMeta(1, path))

	// Brand new: not populated, not fresh.
	if m.IsPopulated(path) {
		t.Fatal("expected !IsPopulated on never-fetched dir")
	}
	if m.IsFresh(path) {
		t.Fatal("expected !IsFresh on never-fetched dir")
	}

	// MarkPopulated then immediately check: both true.
	m.MarkPopulated(path)
	if !m.IsPopulated(path) {
		t.Fatal("expected IsPopulated after MarkPopulated")
	}
	if !m.IsFresh(path) {
		t.Fatal("expected IsFresh immediately after MarkPopulated")
	}

	// Force the timestamp into the past. Reach inside the map to do this
	// rather than sleeping — directoryTTL is 2s and we don't want a
	// 2-second test.
	m.mu.Lock()
	m.populated[path] = time.Now().Add(-directoryTTL - 100*time.Millisecond)
	m.mu.Unlock()

	if !m.IsPopulated(path) {
		t.Fatal("expected still-IsPopulated after timestamp aged (only timestamp moved)")
	}
	if m.IsFresh(path) {
		t.Fatal("expected !IsFresh after timestamp aged past directoryTTL")
	}
}

// TestRemoveRecursive verifies that removing a directory entry also strips
// every cached descendant path — the mechanism by which a phone-side rmdir
// stops haunting the bridge's view after a reconcile.
func TestRemoveRecursive(t *testing.T) {
	m := NewObjectMap()
	m.Put(dirMeta(1, "/storage"))
	m.Put(dirMeta(2, "/storage/sub"))
	m.Put(fileMeta(3, 2, "/storage/sub/a"))
	m.Put(fileMeta(4, 2, "/storage/sub/b"))
	m.Put(dirMeta(5, "/storage/sub/deeper"))
	m.Put(fileMeta(6, 5, "/storage/sub/deeper/c"))
	m.MarkPopulated("/storage")
	m.MarkPopulated("/storage/sub")
	m.MarkPopulated("/storage/sub/deeper")

	// Remove /storage/sub and everything below.
	m.RemoveRecursive("/storage/sub")

	// Sibling outside the subtree should be unaffected.
	if _, ok := m.GetByPath("/storage"); !ok {
		t.Fatal("/storage should still exist")
	}

	// The subtree should be gone, by path and by id, and the populated map
	// should not retain stale entries for any of the removed paths.
	for _, p := range []string{
		"/storage/sub", "/storage/sub/a", "/storage/sub/b",
		"/storage/sub/deeper", "/storage/sub/deeper/c",
	} {
		if _, ok := m.GetByPath(p); ok {
			t.Errorf("%s should be removed from byPath", p)
		}
	}
	for _, id := range []uint32{2, 3, 4, 5, 6} {
		if _, ok := m.GetByID(id); ok {
			t.Errorf("id %d should be removed from byID", id)
		}
	}
	if m.IsPopulated("/storage/sub") || m.IsPopulated("/storage/sub/deeper") {
		t.Error("removed dirs should not be marked populated")
	}
}

// TestRemoveRecursivePrefixSafety guards against a classic bug in
// path-prefix matching: "/foo" and "/foobar" share no logical parent
// relationship, but a naïve HasPrefix("/foo") matches both. Removing
// "/foo" must not touch "/foobar".
func TestRemoveRecursivePrefixSafety(t *testing.T) {
	m := NewObjectMap()
	m.Put(dirMeta(1, "/foo"))
	m.Put(fileMeta(2, 1, "/foo/inside"))
	m.Put(dirMeta(3, "/foobar"))
	m.Put(fileMeta(4, 3, "/foobar/inside"))

	m.RemoveRecursive("/foo")

	if _, ok := m.GetByPath("/foobar"); !ok {
		t.Fatal("/foobar should survive RemoveRecursive(\"/foo\")")
	}
	if _, ok := m.GetByPath("/foobar/inside"); !ok {
		t.Fatal("/foobar/inside should survive RemoveRecursive(\"/foo\")")
	}
	if _, ok := m.GetByPath("/foo"); ok {
		t.Fatal("/foo should be removed")
	}
	if _, ok := m.GetByPath("/foo/inside"); ok {
		t.Fatal("/foo/inside should be removed")
	}
}
