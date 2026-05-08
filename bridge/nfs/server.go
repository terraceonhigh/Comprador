package nfs

import (
	"net"

	gonfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"

	"comprador/bridge/mtp"
)

// Serve starts an NFS server backed by the given MTP session on the provided listener.
// Blocks until the listener is closed. Returns any non-close error.
func Serve(listener net.Listener, session *mtp.Session) error {
	fs := NewMTPFileSystem(session)
	handler := newMTPNFSHandler(fs, session)
	cacheHelper := nfshelper.NewCachingHandler(handler, 1024)
	return gonfs.Serve(listener, cacheHelper)
}
