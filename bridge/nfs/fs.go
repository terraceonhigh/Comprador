package nfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	billy "github.com/go-git/go-billy/v5"

	"comprador/bridge/mtp"
)

// MTPFileSystem implements billy.Filesystem over a live MTP session.
type MTPFileSystem struct {
	session *mtp.Session
	cache   *downloadCache
	writes  *writeRegistry
}

// NewMTPFileSystem creates a billy.Filesystem backed by an MTP session.
func NewMTPFileSystem(session *mtp.Session) *MTPFileSystem {
	return &MTPFileSystem{
		session: session,
		cache:   newDownloadCache(),
		writes:  newWriteRegistry(),
	}
}

// Capabilities advertises read+write so go-nfs's WriteCapability check passes.
func (fs *MTPFileSystem) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.WriteCapability
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

func (fi *mtpFileInfo) Name() string       { return fi.meta.Name }
func (fi *mtpFileInfo) Size() int64        { return int64(fi.meta.Size) }
func (fi *mtpFileInfo) ModTime() time.Time { return fi.meta.ModTime }
func (fi *mtpFileInfo) IsDir() bool        { return fi.meta.IsDir }
func (fi *mtpFileInfo) Sys() interface{}   { return nil }
func (fi *mtpFileInfo) Mode() os.FileMode {
	if fi.meta.IsDir {
		return os.ModeDir | 0755
	}
	return 0644
}

// rootFileInfo is returned for Stat("") / Stat(".").
type rootFileInfo struct{}

func (rootFileInfo) Name() string       { return "/" }
func (rootFileInfo) Size() int64        { return 0 }
func (rootFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (rootFileInfo) IsDir() bool        { return true }
func (rootFileInfo) Sys() interface{}   { return nil }
func (rootFileInfo) Mode() os.FileMode  { return os.ModeDir | 0755 }

// Stat implements billy.Basic. Checks staging area before MTP ObjectMap.
func (fs *MTPFileSystem) Stat(filename string) (os.FileInfo, error) {
	p := cleanPath(filename)
	if p == "/" {
		return rootFileInfo{}, nil
	}
	// Check staging first — a file being written is not in ObjectMap yet.
	if sf := fs.writes.get(p); sf != nil {
		return sf.stat()
	}
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

// Open implements billy.Basic.
func (fs *MTPFileSystem) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

// OpenFile implements billy.Basic.
// If a staging entry exists for the path, all opens (read or write) use it.
// Otherwise write flags are not permitted for existing MTP objects.
func (fs *MTPFileSystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	p := cleanPath(filename)

	if sf := fs.writes.get(p); sf != nil {
		return &stagingHandle{name: filename, sf: sf}, nil
	}

	const writeMask = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC
	if flag&writeMask != 0 {
		return nil, billy.ErrReadOnly
	}

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

// Create registers a staging entry and returns a writable billy.File.
// The file is not sent to MTP until COMMIT.
func (fs *MTPFileSystem) Create(filename string) (billy.File, error) {
	p := cleanPath(filename)
	sf, err := fs.writes.register(p, filename)
	if err != nil {
		return nil, err
	}
	return &stagingHandle{name: filename, sf: sf}, nil
}

// Rename is not yet implemented (MTP has no native rename; requires copy+delete).
func (fs *MTPFileSystem) Rename(_, _ string) error {
	return billy.ErrNotSupported
}

// Remove deletes an MTP object or discards a staging entry.
func (fs *MTPFileSystem) Remove(filename string) error {
	p := cleanPath(filename)

	if sf := fs.writes.delete(p); sf != nil {
		sf.tmp.Close()
		os.Remove(sf.tmp.Name())
		return nil
	}

	fs.session.EnsureInMap(p)
	meta, ok := fs.session.Objects.GetByPath(p)
	if !ok {
		return os.ErrNotExist
	}
	resp := fs.session.Do(mtp.MTPRequest{Op: mtp.OpDelete, ObjectID: meta.ID})
	if resp.Err != nil {
		return resp.Err
	}
	fs.session.Objects.Remove(p)
	return nil
}

// Join implements filepath.Join semantics (required by billy.Basic).
func (fs *MTPFileSystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

// TempFile is not supported.
func (fs *MTPFileSystem) TempFile(dir, prefix string) (billy.File, error) {
	return nil, billy.ErrNotSupported
}

// MkdirAll creates a directory on the MTP device, creating ancestors as needed.
func (fs *MTPFileSystem) MkdirAll(filename string, perm os.FileMode) error {
	return fs.mkdirAll(cleanPath(filename), perm)
}

func (fs *MTPFileSystem) mkdirAll(p string, perm os.FileMode) error {
	if p == "/" {
		return nil
	}
	fs.session.EnsureInMap(p)
	if _, ok := fs.session.Objects.GetByPath(p); ok {
		return nil
	}
	parent := filepath.Dir(p)
	if err := fs.mkdirAll(parent, perm); err != nil {
		return err
	}
	parentMeta, ok := fs.session.Objects.GetByPath(parent)
	if !ok {
		return fmt.Errorf("parent not found after ensure: %s", parent)
	}
	dirName := filepath.Base(p)
	resp := fs.session.Do(mtp.MTPRequest{
		Op:        mtp.OpCreateFolder,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      dirName,
	})
	if resp.Err != nil {
		return resp.Err
	}
	fs.session.Objects.Put(&mtp.ObjectMeta{
		ID:        resp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      dirName,
		Path:      p,
		IsDir:     true,
		ModTime:   time.Now(),
	})
	return nil
}

// Symlink is not supported (MTP has no symlinks).
func (fs *MTPFileSystem) Symlink(_, _ string) error {
	return billy.ErrNotSupported
}

// Readlink is not supported.
func (fs *MTPFileSystem) Readlink(_ string) (string, error) {
	return "", billy.ErrNotSupported
}

// Chroot is not supported.
func (fs *MTPFileSystem) Chroot(_ string) (billy.Filesystem, error) {
	return nil, billy.ErrNotSupported
}

// Root returns "/" — the MTPFileSystem has no chroot offset.
func (fs *MTPFileSystem) Root() string {
	return "/"
}
