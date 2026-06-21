package staging

import (
	"testing"

	"comprador/bridge/mtp"
)

// noopFlush is a flush callback that does nothing — tests drive Commit/Discard
// directly rather than via the idle timer.
func noopFlush(string) {}

// TestCommitRefusesEmptyOverNonEmpty is the regression test for the data-loss
// guard (commit 137df742): an open-without-write or a stray SETATTR(size=0)
// leaves a 0-byte staging temp; committing it would delete a real device file.
// Commit must skip and discard instead, leaving the device object untouched —
// and it must do so BEFORE touching the session (so a nil session here proves
// the guard returns early rather than reaching any device op).
func TestCommitRefusesEmptyOverNonEmpty(t *testing.T) {
	reg := NewRegistry(noopFlush)
	const path = "/Internal shared storage/Documents/Shrek.mp4"

	if _, err := reg.Register(path); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Staging temp has zero bytes (no WriteAt). A non-empty object already
	// exists at this path on the "device".
	objects := mtp.NewObjectMap()
	objects.Put(&mtp.ObjectMeta{ID: 42, Path: path, Name: "Shrek.mp4", Size: 1068042241})

	// nil session: if the guard fails to fire, Commit reaches session.Do and
	// panics — which is exactly the regression we're guarding against.
	if err := reg.Commit(path, nil, objects); err != nil {
		t.Fatalf("Commit should skip an empty-over-non-empty cleanly, got: %v", err)
	}

	if meta, ok := objects.GetByPath(path); !ok || meta.Size != 1068042241 {
		t.Fatalf("device object must survive the skipped commit; got ok=%v meta=%+v", ok, meta)
	}
	if reg.Get(path) != nil {
		t.Fatalf("staging entry should be discarded after the skip")
	}
}

// TestSyntheticHandles covers the handle allocation the FSAL's handle resolver
// and fillStaged depend on: handles start in the high range (above any real
// Android object ID) and are unique per staged path.
func TestSyntheticHandles(t *testing.T) {
	reg := NewRegistry(noopFlush)
	a, err := reg.Register("/s/a.txt")
	if err != nil {
		t.Fatalf("Register a: %v", err)
	}
	b, err := reg.Register("/s/b.txt")
	if err != nil {
		t.Fatalf("Register b: %v", err)
	}
	if a.Handle() < firstSyntheticHandle || b.Handle() < firstSyntheticHandle {
		t.Fatalf("handles must be in the synthetic range (>= %d); got %d, %d",
			firstSyntheticHandle, a.Handle(), b.Handle())
	}
	if a.Handle() == b.Handle() {
		t.Fatalf("handles must be unique; both = %d", a.Handle())
	}
	if p, ok := reg.PathForHandle(a.Handle()); !ok || p != "/s/a.txt" {
		t.Fatalf("PathForHandle(%d) = %q,%v; want /s/a.txt,true", a.Handle(), p, ok)
	}
}

// TestChangeCounterAdvancesOnWrite covers the per-write change counter that
// fillStaged surfaces as the NFSv4 ChangeID, so a client's attribute cache
// invalidates as a staged upload grows.
func TestChangeCounterAdvancesOnWrite(t *testing.T) {
	reg := NewRegistry(noopFlush)
	f, err := reg.Register("/s/c.txt")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	before := f.Change()
	if _, err := f.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if f.Change() <= before {
		t.Fatalf("Change must advance on write: before=%d after=%d", before, f.Change())
	}
	if sz, err := f.Size(); err != nil || sz != 5 {
		t.Fatalf("Size after 5-byte write = %d,%v; want 5,nil", sz, err)
	}
}

// TestDiscardRemovesEntry covers the cleanup path (AppleDouble sidecars, removed
// files): Discard drops the staging entry and its handle mapping.
func TestDiscardRemovesEntry(t *testing.T) {
	reg := NewRegistry(noopFlush)
	f, err := reg.Register("/s/d.txt")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := f.Handle()
	reg.Discard("/s/d.txt")
	if reg.Get("/s/d.txt") != nil {
		t.Fatalf("entry should be gone after Discard")
	}
	if _, ok := reg.PathForHandle(h); ok {
		t.Fatalf("handle mapping should be gone after Discard")
	}
}
