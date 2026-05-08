package nfs

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	billy "github.com/go-git/go-billy/v5"

	"comprador/bridge/mtp"
)

// MTPFileSystem implements billy.Filesystem over a live MTP session.
// Phase 2a: read-only. Write methods return billy.ErrReadOnly.
type MTPFileSystem struct {
	session *mtp.Session
	cache   *downloadCache
}

// NewMTPFileSystem creates a new read-only billy.Filesystem backed by an MTP session.
func NewMTPFileSystem(session *mtp.Session) *MTPFileSystem {
	return &MTPFileSystem{session: session, cache: newDownloadCache()}
}

// cleanPath converts a go-nfs path (no leading slash, "" for root) to the
// MTP ObjectMap format (leading slash, "/" for root).
func cleanPath(p string) string {
	if p == "" || p == "." {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// mtpFileInfo implements os.FileInfo from an ObjectMeta.
type mtpFileInfo struct {
	meta *mtp.ObjectMeta
}

func (fi *mtpFileInfo) Name() string      { return fi.meta.Name }
func (fi *mtpFileInfo) Size() int64       { return int64(fi.meta.Size) }
func (fi *mtpFileInfo) ModTime() time.Time { return fi.meta.ModTime }
func (fi *mtpFileInfo) IsDir() bool        { return fi.meta.IsDir }
func (fi *mtpFileInfo) Sys() interface{}   { return nil }
func (fi *mtpFileInfo) Mode() os.FileMode {
	if fi.meta.IsDir {
		return os.ModeDir | 0555
	}
	return 0444
}

// rootFileInfo is returned for Stat("") / Stat(".").
type rootFileInfo struct{}

func (rootFileInfo) Name() string       { return "/" }
func (rootFileInfo) Size() int64        { return 0 }
func (rootFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (rootFileInfo) IsDir() bool        { return true }
func (rootFileInfo) Sys() interface{}   { return nil }
func (rootFileInfo) Mode() os.FileMode  { return os.ModeDir | 0555 }

// Stat implements billy.Basic. Returns os.ErrNotExist if the path is not on the device.
func (fs *MTPFileSystem) Stat(filename string) (os.FileInfo, error) {
	p := cleanPath(filename)
	if p == "/" {
		return rootFileInfo{}, nil
	}
	// Walk ancestor chain to ensure this entry is in the object map.
	fs.session.EnsureInMap(p)
	meta, ok := fs.session.Objects.GetByPath(p)
	if !ok {
		return nil, os.ErrNotExist
	}
	return &mtpFileInfo{meta: meta}, nil
}

// Lstat is identical to Stat — MTP has no symlinks.
func (fs *MTPFileSystem) Lstat(filename string) (os.FileInfo, error) {
	return fs.Stat(filename)
}

// ReadDir implements billy.Dir.
func (fs *MTPFileSystem) ReadDir(path string) ([]os.FileInfo, error) {
	p := cleanPath(path)
	if p != "/" {
		fs.session.EnsureInMap(p)
	}
	fs.session.EnsurePopulated(p)
	children := fs.session.Objects.ListChildren(p)
	infos := make([]os.FileInfo, len(children))
	for i, meta := range children {
		infos[i] = &mtpFileInfo{meta: meta}
	}
	return infos, nil
}

// Open implements billy.Basic (read-only open).
func (fs *MTPFileSystem) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

// OpenFile implements billy.Basic. Write flags return billy.ErrReadOnly.
func (fs *MTPFileSystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	const writeMask = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC
	if flag&writeMask != 0 {
		return nil, billy.ErrReadOnly
	}
	p := cleanPath(filename)
	fs.session.EnsureInMap(p)
	meta, ok := fs.session.Objects.GetByPath(p)
	if !ok {
		return nil, os.ErrNotExist
	}
	if meta.IsDir {
		return nil, os.ErrInvalid
	}
	return fs.cache.open(meta.Name, meta.ID, fs.session)
}

// Create always returns billy.ErrReadOnly.
func (fs *MTPFileSystem) Create(filename string) (billy.File, error) {
	return nil, billy.ErrReadOnly
}

// Rename always returns billy.ErrReadOnly.
func (fs *MTPFileSystem) Rename(oldpath, newpath string) error {
	return billy.ErrReadOnly
}

// Remove always returns billy.ErrReadOnly.
func (fs *MTPFileSystem) Remove(filename string) error {
	return billy.ErrReadOnly
}

// Join implements filepath.Join semantics (required by billy.Basic).
func (fs *MTPFileSystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

// TempFile always returns billy.ErrNotSupported.
func (fs *MTPFileSystem) TempFile(dir, prefix string) (billy.File, error) {
	return nil, billy.ErrNotSupported
}

// MkdirAll always returns billy.ErrReadOnly.
func (fs *MTPFileSystem) MkdirAll(filename string, perm os.FileMode) error {
	return billy.ErrReadOnly
}

// Symlink always returns billy.ErrNotSupported.
func (fs *MTPFileSystem) Symlink(target, link string) error {
	return billy.ErrNotSupported
}

// Readlink always returns billy.ErrNotSupported.
func (fs *MTPFileSystem) Readlink(link string) (string, error) {
	return "", billy.ErrNotSupported
}

// Chroot always returns billy.ErrNotSupported.
func (fs *MTPFileSystem) Chroot(path string) (billy.Filesystem, error) {
	return nil, billy.ErrNotSupported
}

// Root returns "/" — the MTPFileSystem has no chroot offset.
func (fs *MTPFileSystem) Root() string {
	return "/"
}
