package nfs

import (
	"fmt"
	"log"
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
// The write registry is wired with an idle-flush callback that uploads
// staged writes to MTP when an entry has been quiet for idleFlushInterval.
func NewMTPFileSystem(session *mtp.Session) *MTPFileSystem {
	fs := &MTPFileSystem{session: session, cache: newDownloadCache()}
	fs.writes = newWriteRegistry(func(mtpPath string) {
		if err := fs.writes.commit(mtpPath, fs.session, fs.session.Objects); err != nil {
			log.Printf("idle-flush %s: %v", mtpPath, err)
		} else {
			log.Printf("idle-flush %s: committed", mtpPath)
		}
	})
	return fs
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

// isAppleDoubleBasename reports whether the basename of p starts with "._".
// macOS Finder writes one of these "AppleDouble" companion files alongside
// every file dropped onto a non-HFS+ filesystem to preserve extended
// attributes (Finder labels, resource forks, custom icons). Phones have no
// use for them and the user's "I copied 432 files" expectation is for the
// data files only — the 432-becomes-702 confusion in the 2026-05-11 ECON101
// transfer was almost entirely these. See docs/V0.3.3.md item #3.
//
// Filter only basenames prefixed with "._" — not all dotfiles, since some
// legitimate apps create hidden files (and we already pass through
// non-AppleDouble ones like .git, .hidden_user_doc, etc.).
func isAppleDoubleBasename(p string) bool {
	return strings.HasPrefix(filepath.Base(p), "._")
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
	// Synthetic sentinel files (e.g. /.metadata_never_index) are served
	// directly by the bridge without touching MTP. See sentinels.go.
	if data, ok := sentinelInfo(p); ok {
		return sentinelFileInfo{name: filepath.Base(p), size: int64(len(data))}, nil
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
	infos := make([]os.FileInfo, 0, len(children)+1)
	for _, meta := range children {
		infos = append(infos, &mtpFileInfo{meta: meta})
	}
	// Surface any synthetic sentinel files whose parent is p. The mount
	// root sees /.metadata_never_index so macOS Spotlight skips the
	// volume entirely on first browse. See sentinels.go.
	for spath, data := range sentinelContent {
		if filepath.Dir(spath) == p {
			infos = append(infos, sentinelFileInfo{
				name: filepath.Base(spath),
				size: int64(len(data)),
			})
		}
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

	// Synthetic sentinel files (e.g. /.metadata_never_index) are served
	// directly. Read-only; write flags get the same ErrReadOnly any other
	// MTP-resident object would. See sentinels.go.
	if data, ok := sentinelInfo(p); ok {
		const writeMask = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC
		if flag&writeMask != 0 {
			return nil, billy.ErrReadOnly
		}
		return &sentinelHandle{name: filename, data: data}, nil
	}

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
	// Log every READ-path open so we can see which files macOS is
	// probing (Spotlight, QuickLook, mdworker, FSEvents — any
	// background subsystem that respects .metadata_never_index OR
	// not). Helps diagnose the 2026-05-16 stall.
	log.Printf("OpenFile read-path: path=%q size=%d", p, meta.Size)
	return fs.cache.open(meta.Name, meta.ID, meta.Size, fs.session)
}

// Create registers a staging entry and returns a writable billy.File.
// The file is not sent to MTP until COMMIT.
//
// AppleDouble companion files (`._*` basenames) are accepted by returning
// a discarding handle that silently no-ops writes — the phone never sees
// them. See isAppleDoubleBasename for the rationale; the user-visible
// effect is that a Finder drag-drop of N files produces N entries on the
// phone (not 2N as it did before this filter landed).
func (fs *MTPFileSystem) Create(filename string) (billy.File, error) {
	if isAppleDoubleBasename(filename) {
		return &discardingHandle{name: filename}, nil
	}
	p := cleanPath(filename)
	// Synthetic sentinels are read-only — refuse CREATE on them rather
	// than letting it stage a phantom write that would shadow the virtual
	// content. See sentinels.go.
	if _, ok := sentinelInfo(p); ok {
		return nil, os.ErrPermission
	}
	sf, err := fs.writes.register(p, filename)
	if err != nil {
		return nil, err
	}
	return &stagingHandle{name: filename, sf: sf}, nil
}

// Rename moves a file. Two paths:
//
//   - Fast path (Finder atomic-copy): source is still in the staging
//     registry because Finder writes ".tmpXXXX" then renames before our
//     idle timer fires. Re-key the staging entry under the new name; the
//     timer commits to MTP at the destination name.
//
//   - Slow path: source is already on MTP. Copy the bytes through a
//     temp file (libmtp has no partial read), OpSendFile under the new
//     name, OpDelete the source. Directory rename is not supported in
//     this cut.
//
// MTP itself has no rename op, so this is fundamentally copy + delete on
// the slow path.
func (fs *MTPFileSystem) Rename(oldpath, newpath string) error {
	oldP := cleanPath(oldpath)
	newP := cleanPath(newpath)
	if oldP == newP {
		return nil
	}

	// AppleDouble paths were never staged (Create returned a discarding
	// handle) and aren't on MTP either. A rename touching one would fall
	// through to slow-path copy+delete and fail "src not found". Return
	// success — the file exists nowhere we care about.
	if isAppleDoubleBasename(oldP) || isAppleDoubleBasename(newP) {
		return nil
	}

	// Fast path: staging-resident source.
	if fs.writes.rekey(oldP, newP, strings.TrimPrefix(newpath, "/")) {
		return nil
	}

	// Slow path: MTP-resident source.
	fs.session.EnsureInMap(oldP)
	srcMeta, ok := fs.session.Objects.GetByPath(oldP)
	if !ok {
		return os.ErrNotExist
	}
	if srcMeta.IsDir {
		return fmt.Errorf("rename of directories not supported in this build")
	}

	destParent := cleanPath(filepath.Dir(strings.TrimPrefix(newpath, "/")))
	fs.session.EnsureInMap(destParent)
	parentMeta, ok := fs.session.Objects.GetByPath(destParent)
	if !ok {
		return os.ErrNotExist
	}

	// POSIX rename overwrites an existing destination.
	if existing, ok := fs.session.Objects.GetByPath(newP); ok {
		resp := fs.session.Do(mtp.MTPRequest{Op: mtp.OpDelete, ObjectID: existing.ID})
		if resp.Err != nil {
			return fmt.Errorf("rename: delete existing destination: %w", resp.Err)
		}
		fs.session.Objects.Remove(newP)
	}

	tmp, err := os.CreateTemp("", "comprador-rename-*")
	if err != nil {
		return fmt.Errorf("rename: temp file: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	getResp := fs.session.Do(mtp.MTPRequest{Op: mtp.OpGetFile, ObjectID: srcMeta.ID, Writer: tmp})
	if getResp.Err != nil {
		return fmt.Errorf("rename: read source: %w", getResp.Err)
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return fmt.Errorf("rename: seek temp: %w", err)
	}

	fileName := filepath.Base(newP)
	sendResp := fs.session.Do(mtp.MTPRequest{
		Op:        mtp.OpSendFile,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      fileName,
		Size:      srcMeta.Size,
		Reader:    tmp,
	})
	if sendResp.Err != nil {
		return fmt.Errorf("rename: write destination: %w", sendResp.Err)
	}

	delResp := fs.session.Do(mtp.MTPRequest{Op: mtp.OpDelete, ObjectID: srcMeta.ID})
	if delResp.Err != nil {
		// Source delete failed; we have the file at both ends. Update the
		// map for the destination so subsequent ops see it; the source
		// remains visible until next session refresh.
		fs.session.Objects.Put(&mtp.ObjectMeta{
			ID:        sendResp.ObjectID,
			ParentID:  parentMeta.ID,
			StorageID: parentMeta.StorageID,
			Name:      fileName,
			Path:      newP,
			Size:      srcMeta.Size,
			ModTime:   time.Now(),
			IsDir:     false,
		})
		return fmt.Errorf("rename: delete source after copy: %w", delResp.Err)
	}

	fs.session.Objects.Remove(oldP)
	fs.session.Objects.Put(&mtp.ObjectMeta{
		ID:        sendResp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      fileName,
		Path:      newP,
		Size:      srcMeta.Size,
		ModTime:   time.Now(),
		IsDir:     false,
	})

	// Bump both source-parent and dest-parent dir mtimes so Finder
	// re-enumerates both directories.
	srcParentP := cleanPath(filepath.Dir(strings.TrimPrefix(oldpath, "/")))
	if srcParent, ok := fs.session.Objects.GetByPath(srcParentP); ok {
		bumpDirMtime(srcParent, fs.session.Objects)
	}
	bumpDirMtime(parentMeta, fs.session.Objects)
	return nil
}

// Remove deletes an MTP object or discards a staging entry.
func (fs *MTPFileSystem) Remove(filename string) error {
	p := cleanPath(filename)

	// Synthetic sentinels cannot be removed — they're not on the device.
	// Refuse explicitly rather than fall through to ObjectMap and surface
	// ErrNotExist (which would be misleading; the file does exist from
	// the client's perspective). See sentinels.go.
	if _, ok := sentinelInfo(p); ok {
		return os.ErrPermission
	}

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

	// Bump the parent dir's mtime so Finder re-enumerates and the
	// removed file disappears from the listing without waiting for
	// the NFS client's attribute-cache TTL to expire.
	parentP := cleanPath(filepath.Dir(strings.TrimPrefix(filename, "/")))
	if parent, ok := fs.session.Objects.GetByPath(parentP); ok {
		bumpDirMtime(parent, fs.session.Objects)
	}
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
	// Bump the parent directory's mtime so Finder/the NFS client
	// re-enumerates and surfaces the new subdirectory immediately.
	// Without this, recursive folder drags stutter — Finder issues
	// MKDIR for an intermediate dir, then CREATE for files inside,
	// but the parent listing isn't refreshed until the first file
	// commits, which can confuse Finder mid-copy.
	bumpDirMtime(parentMeta, fs.session.Objects)
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
