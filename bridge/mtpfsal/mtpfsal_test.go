package mtpfsal

import (
	"context"
	"testing"

	"github.com/terraceonhigh/galatea/pkg/virtual"

	"comprador/bridge/mtp"
)

// TestSetMandatoryAttrsFloorsSize locks the chokepoint contract: setMandatoryAttrs
// must leave SIZE *set* (Galatea's encoder panics on requested-but-unset
// FATTR4_SIZE, exactly like the named-attr flags). A fresh Attributes must come
// out with size present.
func TestSetMandatoryAttrsFloorsSize(t *testing.T) {
	var a virtual.Attributes
	setMandatoryAttrs(&a, 42)

	if _, ok := a.GetSizeBytes(); !ok {
		t.Fatal("setMandatoryAttrs left SIZE unset — Galatea's encoder panics on requested-but-unset FATTR4_SIZE")
	}
	if len(a.GetFileHandle()) == 0 { // GetFileHandle panics if unset, so this also guards presence
		t.Fatal("setMandatoryAttrs left the file handle empty")
	}
}

// TestSetMandatoryAttrsDoesNotClobberSize is the other half: the floor is applied
// ONLY when no size is set. The committed file/dir paths call SetSizeBytes BEFORE
// fillCommon, so an unconditional floor would zero every real file's size back to
// 0 — the Finder-can't-open bug. A size already present must survive.
func TestSetMandatoryAttrsDoesNotClobberSize(t *testing.T) {
	var a virtual.Attributes
	a.SetSizeBytes(123456)
	setMandatoryAttrs(&a, 42)

	if sz, ok := a.GetSizeBytes(); !ok || sz != 123456 {
		t.Fatalf("setMandatoryAttrs clobbered a real size: got (%d, %v), want (123456, true)", sz, ok)
	}
}

// TestVanishedFileReportsSize drives the real tombstone branch of
// VirtualGetAttributes — a GETATTR on a path that no longer exists in the object
// map (concurrent rename/move/delete between resolve and GETATTR). This once
// crashed the whole bridge: the branch set the mandatory handle/flags but left
// SIZE unset, and Galatea panicked encoding the reply ("FATTR4_SIZE is a required
// attribute"). The fix routes the floor through setMandatoryAttrs; assert size is
// present after the vanished-path GETATTR.
func TestVanishedFileReportsSize(t *testing.T) {
	sess := &mtp.Session{Objects: mtp.NewObjectMap()} // empty map: every path "vanishes"
	f := &mtpFile{node{session: sess, reg: nil, mpath: "/DCIM/gone.jpg"}}

	var a virtual.Attributes
	f.VirtualGetAttributes(context.Background(),
		virtual.AttributesMaskSizeBytes|virtual.AttributesMaskFileHandle|virtual.AttributesMaskChangeID, &a)

	if _, ok := a.GetSizeBytes(); !ok {
		t.Fatal("tombstone GETATTR left SIZE unset — would panic Galatea's encoder")
	}
	if len(a.GetFileHandle()) == 0 {
		t.Fatal("tombstone GETATTR left the file handle empty")
	}
}
