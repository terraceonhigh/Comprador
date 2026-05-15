package nfs

import (
	"context"
	"log"
	"net"
	"strings"

	billy "github.com/go-git/go-billy/v5"
	gonfs "github.com/willscott/go-nfs"

	"comprador/bridge/mtp"
)

// mtpNFSHandler implements gonfs.Handler for an MTP-backed filesystem.
// ToHandle/FromHandle/InvalidateHandle/HandleLimit are overridden by the
// CachingHandler wrapper; the implementations here satisfy the interface only.
type mtpNFSHandler struct {
	fs      *MTPFileSystem
	session *mtp.Session
}

func newMTPNFSHandler(fs *MTPFileSystem, session *mtp.Session) gonfs.Handler {
	return &mtpNFSHandler{fs: fs, session: session}
}

func (h *mtpNFSHandler) Mount(_ context.Context, _ net.Conn, _ gonfs.MountRequest) (gonfs.MountStatus, billy.Filesystem, []gonfs.AuthFlavor) {
	return gonfs.MountStatusOk, h.fs, []gonfs.AuthFlavor{gonfs.AuthFlavorNull}
}

// Change returns a no-op billy.Change so that go-nfs's SetFileAttributes.Apply
// can succeed when macOS sets chmod/mtime on a newly created file.
// MTP has no Unix permission model; all attribute changes are silently accepted.
func (h *mtpNFSHandler) Change(_ billy.Filesystem) billy.Change {
	return noopChange{}
}

// FSStat reports per-storage quota when the requesting path lives under a
// specific storage subtree, falling back to aggregate when called at the
// mount root or against an unknown first segment. This matters because
// macOS's preflight free-space check (statfs(2) under the hood) drives
// Finder's "X GB available" string and its drop-onto-volume copy gate;
// aggregate reporting across mixed-size storages produces the cardinal sin
// in docs/PLAN-MULTI-STORAGE.md — green-lighting a 50 GB drop because
// "105 GB free" sums Internal + SD, when the user is actually standing in
// a near-full SD card.
//
// Refreshes the storage list on each call. Per docs/PLAN-MULTI-STORAGE.md
// §3, FSStat is called infrequently enough (~once per Finder focus, once
// per copy preflight) that the libmtp re-query cost is acceptable in
// exchange for fresh numbers visible to the user.
func (h *mtpNFSHandler) FSStat(_ context.Context, _ billy.Filesystem, path []string, s *gonfs.FSStat) error {
	if err := h.session.RefreshStorages(); err != nil {
		log.Printf("FSStat: RefreshStorages failed (%v); reporting cached values", err)
	}

	if storage := h.session.StorageForPath(path); storage != nil {
		s.TotalSize = storage.MaxBytes
		s.FreeSize = storage.FreeBytes
		s.AvailableSize = storage.FreeBytes
		log.Printf("FSStat path=%v → storage=%q free=%d/total=%d",
			path, storage.Description, storage.FreeBytes, storage.MaxBytes)
		return nil
	}

	s.TotalSize = h.session.TotalBytes()
	s.FreeSize = h.session.FreeBytes()
	s.AvailableSize = h.session.FreeBytes()
	log.Printf("FSStat path=%v → aggregate (no storage match) free=%d/total=%d",
		path, s.FreeSize, s.TotalSize)
	return nil
}

// Commit is called when the NFS client issues a COMMIT RPC for the file at
// path. This is the signal that all WRITE RPCs are done; we flush the staging
// temp file to MTP here.
func (h *mtpNFSHandler) Commit(_ context.Context, fs billy.Filesystem, path []string) error {
	mtpFS, ok := fs.(*MTPFileSystem)
	if !ok {
		return nil
	}
	mtpPath := cleanPath(strings.Join(path, "/"))
	sf := mtpFS.writes.get(mtpPath)
	if sf == nil {
		return nil // no pending entry; either already committed or never staged
	}
	if err := sf.commitOnce(true /* wait */); err != nil {
		log.Printf("COMMIT %s: %v", mtpPath, err)
		return err
	}
	return nil
}

// The following three methods are overridden by CachingHandler; these
// implementations are unreachable in normal operation.
func (h *mtpNFSHandler) ToHandle(_ billy.Filesystem, _ []string) []byte { return []byte{} }
func (h *mtpNFSHandler) FromHandle(_ []byte) (billy.Filesystem, []string, error) {
	return nil, []string{}, nil
}
func (h *mtpNFSHandler) InvalidateHandle(_ billy.Filesystem, _ []byte) error { return nil }
func (h *mtpNFSHandler) HandleLimit() int                                     { return -1 }
