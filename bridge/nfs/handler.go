package nfs

import (
	"context"
	"net"

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

// Change returns nil — read-only for Phase 2a. go-nfs returns EROFS for any
// SETATTR calls that try to modify permissions or ownership.
func (h *mtpNFSHandler) Change(_ billy.Filesystem) billy.Change {
	return nil
}

func (h *mtpNFSHandler) FSStat(_ context.Context, _ billy.Filesystem, s *gonfs.FSStat) error {
	s.TotalSize = h.session.TotalBytes()
	s.FreeSize = h.session.FreeBytes()
	s.AvailableSize = h.session.FreeBytes()
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
