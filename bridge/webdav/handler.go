package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"comprador/bridge/mtp"
	"comprador/bridge/resume"

	"golang.org/x/net/webdav"
)

// fcntlNoCache turns off the unified buffer cache for reads from the
// given file descriptor. macOS-specific (F_NOCACHE = 48). Used on
// staging files we're about to stream to MTP — every page we'd
// otherwise read into the cache stays attributed to our process's
// physical footprint until the kernel reclaims it under pressure,
// and on an 8 GiB Mac dragging a 9 GiB file that cache fill *is*
// the memory pressure that triggers webdavfs's writeseq cap.
//
// Best-effort: a failure here is non-fatal (we just take the cache
// hit). Logged once per open at debug-info level.
func fcntlNoCache(f *os.File) {
	const F_NOCACHE = 48
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), F_NOCACHE, 1)
	if errno != 0 {
		log.Printf("F_NOCACHE on %s failed: %v (continuing with cached I/O)", f.Name(), errno)
	}
}

// expectedLengthKey carries the X-Expected-Entity-Length value from the PUT
// request handler down to the FileSystem. Apple's WebDAVFS client sends this
// header on chunked uploads to advertise the full file size (Content-Length
// is unknown when chunked encoding is used). The kernel can silently
// truncate the chunked body at 32 or 64 MiB depending on memory pressure —
// see WEBDAVIOC_WRITE_SEQUENTIAL in
// https://github.com/apple-oss-distributions/webdavfs. The FileSystem
// compares X-Expected-Entity-Length against the bytes actually received
// and refuses to commit truncated uploads, so the existing file is
// preserved and the user sees a clean error instead of a silent half-write.
type contextKey string

const expectedLengthKey contextKey = "x-expected-entity-length"

// NewHandler creates an http.Handler that serves an MTP device over WebDAV.
//
// `store` is optional — pass nil to disable resumable-upload support.
// When provided, truncated chunked PUTs (Apple WebDAVFS writeseq cap)
// are persisted into the store and made resumable via the
// /_comprador/sessions/* endpoints. See docs/RESUMABLE-UPLOADS.md.
func NewHandler(session *mtp.Session, store *resume.Store) http.Handler {
	filesystem := &mtpFS{session: session, store: store}
	lockSystem := &noopLockSystem{}

	h := &webdav.Handler{
		FileSystem: filesystem,
		LockSystem: lockSystem,
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("WebDAV %s %s → error: %v", r.Method, r.URL.Path, err)
			}
		},
	}

	var resumeH http.Handler
	if store != nil {
		resumeH = &resumeEndpoint{store: store, session: session}
	}

	return &finderHandler{
		inner:      h,
		resume:     resumeH,
		filesystem: filesystem,
	}
}

// finderHandler wraps the standard WebDAV handler with Finder-specific
// quirk handling and routes the /_comprador/* prefix to the resume
// endpoint instead of the WebDAV layer.
type finderHandler struct {
	inner      http.Handler
	resume     http.Handler // nil if resumable uploads are disabled
	filesystem *mtpFS       // direct ref for the keepalive PUT path
}

func (fh *finderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Internal Comprador-only prefix — not part of the WebDAV namespace,
	// invisible to webdavfs. Used by the menu-bar app to drive resumable
	// uploads.
	if fh.resume != nil && strings.HasPrefix(r.URL.Path, "/_comprador/") {
		fh.resume.ServeHTTP(w, r)
		return
	}

	reqPath := cleanPath(r.URL.Path)

	// Intercept Finder probe files — return 404 without touching MTP
	if isFinderProbe(reqPath) {
		http.NotFound(w, r)
		return
	}

	// Carry X-Expected-Entity-Length (Apple WebDAVFS proprietary header)
	// down to the FileSystem layer so OpenFile/Close can detect when the
	// kernel client truncated the chunked body and refuse to commit the
	// partial upload. The header is sent with chunked PUTs from
	// /System/Library/Extensions/webdav_fs.kext's userspace agent.
	if r.Method == "PUT" {
		if v := r.Header.Get("X-Expected-Entity-Length"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				r = r.WithContext(context.WithValue(r.Context(), expectedLengthKey, n))
				// Body PUT with declared size — long MTP send to follow.
				// Take over the connection and emit 102 Processing
				// keepalives so webdavfs's PUT-response timeout (~60s)
				// doesn't fire while libmtp is mid-SendFile (multi-min
				// for multi-GiB files). Placeholder PUTs (no
				// X-Expected-Entity-Length) are fast — the inner handler
				// handles them.
				fh.servePutWithKeepalive(w, r, n)
				return
			}
		}
	}

	fh.inner.ServeHTTP(w, r)
}

// servePutWithKeepalive owns the slow chunked-PUT path. It does the
// same work as `webdav.Handler.handlePut` minus features we don't
// actually use (real LOCK confirmation, Content-Range, ETag matching),
// then hijacks the underlying TCP connection and emits HTTP/1.1 102
// Processing interim responses every 30s while libmtp's SendFile
// works through the multi-minute USB transfer.
//
// Why this path exists: webdavfs's PUT-response timeout (~60s)
// fires long before the MTP send for a multi-GiB file completes.
// Without keepalives, Finder shows -36 even when the upload
// successfully lands on the phone — the file appears, but the user
// is told it failed. 102 Processing is the HTTP/1.1 server's way
// of saying "still working, hold the line." Whether webdavfs's
// CFNetwork stack actually treats it as a timeout reset is the
// experiment we're running.
//
// We only take over PUTs with an X-Expected-Entity-Length header —
// that's webdavfs's marker for the chunked body PUT (the slow one).
// Placeholder PUTs (Content-Length: 0, no expected-length) are fast
// and stay on the inner handler's path.
func (fh *finderHandler) servePutWithKeepalive(w http.ResponseWriter, r *http.Request, expectedSize int64) {
	name := cleanPath(r.URL.Path)

	// Open the destination via the FileSystem. Same flags webdav.Handler
	// uses for PUT.
	ctx := r.Context()
	openF, err := fh.filesystem.OpenFile(ctx, name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		log.Printf("keepalive PUT %s: OpenFile: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Stream the body into the FileSystem object. This is the fast part —
	// loopback delivery + disk write. For a 9 GiB body it takes ~1 minute
	// at observed Mac SSD throughput.
	if _, err := io.Copy(openF, r.Body); err != nil {
		_ = openF.Close()
		log.Printf("keepalive PUT %s: body copy: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Past this point we need to call openF.Close(), which triggers the
	// long MTP SendFile. Hijack the connection so we can write 102s
	// while it runs. If the http.ResponseWriter doesn't support hijack
	// (shouldn't happen for the default Go HTTP/1.1 server), fall back
	// to the synchronous path — Finder will probably -36 but the data
	// will land.
	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("keepalive PUT %s: ResponseWriter not hijackable, falling back to sync close", name)
		closeErr := openF.Close()
		if closeErr != nil {
			http.Error(w, closeErr.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		log.Printf("keepalive PUT %s: Hijack failed: %v", name, err)
		closeErr := openF.Close()
		if closeErr != nil {
			http.Error(w, closeErr.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	}
	defer conn.Close()

	// Run openF.Close() (the MTP SendFile) in a goroutine; the main
	// goroutine handles the keepalive ticker and the final response.
	done := make(chan error, 1)
	go func() {
		done <- openF.Close()
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	startedAt := time.Now()

	for {
		select {
		case closeErr := <-done:
			if closeErr != nil {
				log.Printf("keepalive PUT %s: SendFile failed after %s: %v",
					name, time.Since(startedAt).Round(time.Second), closeErr)
				_, _ = bufrw.WriteString("HTTP/1.1 500 Internal Server Error\r\n" +
					"Content-Length: 0\r\n" +
					"Connection: close\r\n\r\n")
				_ = bufrw.Flush()
				return
			}
			log.Printf("keepalive PUT %s: SendFile complete after %s, %d 102s sent (expected %d bytes)",
				name, time.Since(startedAt).Round(time.Second),
				int(time.Since(startedAt)/(30*time.Second)), expectedSize)
			_, _ = bufrw.WriteString("HTTP/1.1 201 Created\r\n" +
				"Content-Length: 0\r\n" +
				"Connection: close\r\n\r\n")
			_ = bufrw.Flush()
			return
		case <-ticker.C:
			// HTTP/1.1 102 Processing — interim response. Whether
			// webdavfs treats this as a timeout reset is what we're
			// finding out. RFC 7231 says intermediaries and clients
			// MUST be prepared to receive one or more 1xx responses
			// before the final response, so this is well-formed.
			if _, err := bufrw.WriteString("HTTP/1.1 102 Processing\r\n\r\n"); err != nil {
				log.Printf("keepalive PUT %s: write 102 failed (client disconnected?): %v",
					name, err)
				// Wait for SendFile to finish; without the conn we
				// can't reply, but the file may still land on the phone.
				<-done
				return
			}
			if err := bufrw.Flush(); err != nil {
				log.Printf("keepalive PUT %s: flush 102 failed: %v", name, err)
				<-done
				return
			}
		}
	}
}

// mtpFS implements webdav.FileSystem backed by an MTP session.
type mtpFS struct {
	session *mtp.Session
	store   *resume.Store // nil disables resumable-upload persistence
}

func (mfs *mtpFS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	name = cleanPath(name)
	parent := path.Dir(name)
	base := path.Base(name)

	parentMeta, ok := mfs.session.Objects.GetByPath(parent)
	if !ok {
		return os.ErrNotExist
	}

	resp := mfs.session.Do(mtp.MTPRequest{
		Op:        mtp.OpCreateFolder,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      base,
	})
	if resp.Err != nil {
		return resp.Err
	}

	mfs.session.Objects.Put(&mtp.ObjectMeta{
		ID:        resp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      base,
		Path:      name,
		IsDir:     true,
		ModTime:   time.Now(),
	})
	// Deliberately no InvalidateDir(parent) here. The Put above keeps
	// the object map correct, and OpenFile already called
	// EnsurePopulated(parent) before this Mkdir, so the parent's
	// children list is already complete + the new dir we just added.
	// Invalidating would force a session-goroutine round-trip on the
	// next PROPFIND, which queues behind any in-flight GET — exactly
	// the race that gave us the "Finder shows 0 items after upload"
	// glitch. See the milestone/2026-05-06-memory-verified
	// commit-after notes.
	return nil
}

func (mfs *mtpFS) RemoveAll(_ context.Context, name string) error {
	name = cleanPath(name)
	meta, ok := mfs.session.Objects.GetByPath(name)
	if !ok {
		return os.ErrNotExist
	}

	// Delete children first if directory
	if meta.IsDir {
		mfs.session.EnsurePopulated(name)
		children := mfs.session.Objects.ListChildren(name)
		for _, child := range children {
			if err := mfs.RemoveAll(context.Background(), child.Path); err != nil {
				return err
			}
		}
	}

	resp := mfs.session.Do(mtp.MTPRequest{
		Op:       mtp.OpDelete,
		ObjectID: meta.ID,
	})
	if resp.Err != nil {
		return resp.Err
	}
	mfs.session.Objects.Remove(name)
	// Same rationale as Mkdir: Remove keeps the object map correct;
	// the parent's populated state still describes a valid (smaller)
	// listing. Skip InvalidateDir to avoid the post-write
	// re-enumeration round-trip.
	return nil
}

func (mfs *mtpFS) Rename(_ context.Context, oldName, newName string) error {
	// MTP has no rename. Copy + delete.
	oldName = cleanPath(oldName)
	newName = cleanPath(newName)

	meta, ok := mfs.session.Objects.GetByPath(oldName)
	if !ok {
		return os.ErrNotExist
	}
	if meta.IsDir {
		return &os.PathError{Op: "rename", Path: oldName, Err: os.ErrPermission}
	}

	// Read file into memory
	var buf bytes.Buffer
	resp := mfs.session.Do(mtp.MTPRequest{
		Op:       mtp.OpGetFile,
		ObjectID: meta.ID,
		Writer:   &buf,
	})
	if resp.Err != nil {
		return resp.Err
	}

	// Determine destination parent
	newParent := path.Dir(newName)
	newBase := path.Base(newName)
	parentMeta, ok := mfs.session.Objects.GetByPath(newParent)
	if !ok {
		return os.ErrNotExist
	}

	// Upload to new location
	reader := bytes.NewReader(buf.Bytes())
	sendResp := mfs.session.Do(mtp.MTPRequest{
		Op:        mtp.OpSendFile,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      newBase,
		Size:      uint64(buf.Len()),
		Reader:    reader,
	})
	if sendResp.Err != nil {
		return sendResp.Err
	}

	// Add new entry to object map
	mfs.session.Objects.Put(&mtp.ObjectMeta{
		ID:        sendResp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      newBase,
		Path:      newName,
		Size:      meta.Size,
		ModTime:   meta.ModTime,
		IsDir:     false,
	})

	// Delete old
	delResp := mfs.session.Do(mtp.MTPRequest{
		Op:       mtp.OpDelete,
		ObjectID: meta.ID,
	})
	if delResp.Err != nil {
		return delResp.Err
	}
	mfs.session.Objects.Remove(oldName)
	// Same rationale as Mkdir / RemoveAll: the Put + Remove above
	// keep both source and destination parents' object maps correct.
	// No InvalidateDir.
	return nil
}

func (mfs *mtpFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	name = cleanPath(name)

	// Root directory
	if name == "/" {
		return &mtpDir{
			session: mfs.session,
			path:    "/",
			meta: &mtp.ObjectMeta{
				Path:    "/",
				Name:    "/",
				IsDir:   true,
				ModTime: time.Now(),
			},
		}, nil
	}

	// Ensure the parent directory is populated so this path is in the cache
	parent := path.Dir(name)
	mfs.session.EnsurePopulated(parent)

	meta, ok := mfs.session.Objects.GetByPath(name)

	// Handle file creation or overwrite (PUT)
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
		if !ok || !meta.IsDir {
			f := &mtpNewFile{
				session: mfs.session,
				store:   mfs.store,
				path:    name,
			}
			// Replace flow: remember the existing object so we can
			// delete it on a successful upload. We deliberately don't
			// delete here, because if the upload turns out to be
			// truncated (Apple WebDAVFS writeseq cap), we want to
			// preserve the existing file rather than replace it with
			// a partial one.
			if ok && !meta.IsDir {
				existingID := meta.ID
				f.existingID = &existingID
			}
			// Pull the expected size from context, if the request
			// carried X-Expected-Entity-Length.
			if v, ok := ctx.Value(expectedLengthKey).(int64); ok {
				f.expectedSize = v
			}
			return f, nil
		}
	}

	if !ok {
		return nil, os.ErrNotExist
	}

	if meta.IsDir {
		return &mtpDir{
			session: mfs.session,
			path:    name,
			meta:    meta,
		}, nil
	}

	return &mtpFile{
		session: mfs.session,
		meta:    meta,
	}, nil
}

func (mfs *mtpFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	name = cleanPath(name)

	if name == "/" {
		return &mtpFileInfo{
			name:    "/",
			size:    0,
			modTime: time.Now(),
			isDir:   true,
		}, nil
	}

	// Ensure parent is populated so this entry exists in the cache
	parent := path.Dir(name)
	mfs.session.EnsurePopulated(parent)

	meta, ok := mfs.session.Objects.GetByPath(name)
	if !ok {
		return nil, os.ErrNotExist
	}

	return metaToFileInfo(meta), nil
}

// mtpDir represents an open MTP directory.
type mtpDir struct {
	session  *mtp.Session
	path     string
	meta     *mtp.ObjectMeta
	children []os.FileInfo
	pos      int
}

func (d *mtpDir) Close() error                                 { return nil }
func (d *mtpDir) Read(_ []byte) (int, error)                   { return 0, os.ErrInvalid }
func (d *mtpDir) Write(_ []byte) (int, error)                  { return 0, os.ErrInvalid }
func (d *mtpDir) Seek(_ int64, _ int) (int64, error)           { return 0, os.ErrInvalid }

func (d *mtpDir) Stat() (os.FileInfo, error) {
	return metaToFileInfo(d.meta), nil
}

// DeadProps satisfies webdav.DeadPropsHolder so we can advertise the device's
// free/total capacity via DAV:quota-available-bytes and DAV:quota-used-bytes.
// macOS webdavfs translates these into statfs(2) results, which Finder
// preflight-checks before starting a copy. Without this, Finder reports
// "(error code 100060)" and bails before sending a single byte — the bridge's
// PUT path is never reached, so the upload fix below it is moot.
//
// Only the mount root ("/") returns quota; subdirectories return nil so the
// webdav package falls back to file-level metadata. Reporting on the root is
// what statfs hits, and what Finder uses for its preflight.
func (d *mtpDir) DeadProps() (map[xml.Name]webdav.Property, error) {
	if d.path != "/" {
		return nil, nil
	}
	total := d.session.TotalBytes()
	free := d.session.FreeBytes()
	if total == 0 {
		return nil, nil
	}
	used := total - free
	return map[xml.Name]webdav.Property{
		{Space: "DAV:", Local: "quota-available-bytes"}: {
			XMLName:  xml.Name{Space: "DAV:", Local: "quota-available-bytes"},
			InnerXML: []byte(strconv.FormatUint(free, 10)),
		},
		{Space: "DAV:", Local: "quota-used-bytes"}: {
			XMLName:  xml.Name{Space: "DAV:", Local: "quota-used-bytes"},
			InnerXML: []byte(strconv.FormatUint(used, 10)),
		},
	}, nil
}

// Patch is required by webdav.DeadPropsHolder. Quota is read-only — refuse
// any attempt to mutate it (Finder shouldn't try, but the package needs the
// method to exist).
func (d *mtpDir) Patch(_ []webdav.Proppatch) ([]webdav.Propstat, error) {
	return nil, webdav.ErrForbidden
}

func (d *mtpDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.children == nil {
		// Lazily populate this directory from the device
		d.session.EnsurePopulated(d.path)
		entries := d.session.Objects.ListChildren(d.path)
		d.children = make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			d.children = append(d.children, metaToFileInfo(e))
		}
	}

	if count <= 0 {
		if d.pos >= len(d.children) {
			return nil, nil
		}
		result := d.children[d.pos:]
		d.pos = len(d.children)
		return result, nil
	}

	if d.pos >= len(d.children) {
		return nil, io.EOF
	}
	end := d.pos + count
	if end > len(d.children) {
		end = len(d.children)
	}
	result := d.children[d.pos:end]
	d.pos = end
	if d.pos >= len(d.children) {
		return result, io.EOF
	}
	return result, nil
}

// mtpFile represents an open MTP file for reading.
//
// On first Read/Seek, the entire file is streamed from the device to a
// staging file on disk (with F_NOCACHE on the read fd, see below) and
// subsequent operations serve from that file. The fetch is pull-based
// because libmtp's Get_File_To_Handler delivers the whole stream in
// one call; we can't return partial reads to the WebDAV layer mid-MTP.
//
// The pre-staging implementation accumulated the entire file in a
// `bytes.Buffer`, which on a multi-GiB phone file would peg the bridge
// process at file-size physical footprint and push low-RAM Macs into
// swap thrash — exactly the same anti-pattern the streaming-write
// refactor (commit 0c5a18e) fixed for the upload (`mtpNewFile`) path.
// Keeping the read path unchanged was an oversight; this commit closes
// it. Symptom that surfaced the gap: Finder QuickLook on a leftover
// 9 GiB Attenborough.mkv triggered a full Get → bridge buffered the
// whole file in `mtpFile.ensureFetched()`'s `bytes.Buffer` → bridge
// hit 10 GB physical footprint with 9.9 GB swapped, blocking every
// other MTP op for ~15-20 minutes.
type mtpFile struct {
	session *mtp.Session
	meta    *mtp.ObjectMeta

	// Set on first Read/Seek by ensureFetched. The staging file is
	// deleted in Close.
	stagingPath string
	stagingFile *os.File
}

func (f *mtpFile) Close() error {
	if f.stagingFile != nil {
		_ = f.stagingFile.Close()
		f.stagingFile = nil
	}
	if f.stagingPath != "" {
		_ = os.Remove(f.stagingPath)
		f.stagingPath = ""
	}
	return nil
}

func (f *mtpFile) Write(_ []byte) (int, error) {
	return 0, os.ErrPermission
}

func (f *mtpFile) ensureFetched() error {
	if f.stagingFile != nil {
		return nil
	}

	// Create the staging tempfile. Use a dedicated subdir under
	// $TMPDIR so the file is on local disk (not network FS) and
	// gets cleaned by macOS's temp reaper if we crash before Close.
	staging, err := os.CreateTemp("", "comprador-fetch-*.tmp")
	if err != nil {
		return fmt.Errorf("ensureFetched create staging: %w", err)
	}
	stagingPath := staging.Name()

	// Stream the entire file from the device into the staging file.
	// libmtp's Get_File_To_Handler doesn't support partial / range
	// reads, so we have to materialize the whole thing before serving.
	resp := f.session.Do(mtp.MTPRequest{
		Op:       mtp.OpGetFile,
		ObjectID: f.meta.ID,
		Writer:   staging,
	})
	closeErr := staging.Close()
	if resp.Err != nil {
		os.Remove(stagingPath)
		return resp.Err
	}
	if closeErr != nil {
		os.Remove(stagingPath)
		return fmt.Errorf("ensureFetched close staging: %w", closeErr)
	}

	// Reopen for reading with F_NOCACHE so subsequent Reads bypass the
	// unified buffer cache. Without this, every page we read would be
	// retained as cache attributed to our process's physical footprint
	// until kernel reclaim — the same trap the upload path hits.
	body, err := os.Open(stagingPath)
	if err != nil {
		os.Remove(stagingPath)
		return fmt.Errorf("ensureFetched reopen staging: %w", err)
	}
	fcntlNoCache(body)

	f.stagingPath = stagingPath
	f.stagingFile = body
	return nil
}

func (f *mtpFile) Read(p []byte) (int, error) {
	if err := f.ensureFetched(); err != nil {
		return 0, err
	}
	return f.stagingFile.Read(p)
}

func (f *mtpFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.ensureFetched(); err != nil {
		return 0, err
	}
	return f.stagingFile.Seek(offset, whence)
}

func (f *mtpFile) Stat() (os.FileInfo, error) {
	return metaToFileInfo(f.meta), nil
}

func (f *mtpFile) Readdir(_ int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

// mtpNewFile handles PUT (file creation/upload).
//
// expectedSize is non-zero when the PUT carried X-Expected-Entity-Length
// (Apple WebDAVFS sends this with chunked uploads). If the body we receive
// is shorter than expectedSize, the kernel client truncated the upload —
// we either persist for resume (when companion is alive) or refuse with
// the existing file preserved.
//
// existingID is non-nil when this PUT is replacing an existing object. We
// defer the delete to Close so that a failed (truncated) upload doesn't
// destroy the previous file.
//
// Body bytes are streamed directly to a staging file on disk via
// `tempFile`, not accumulated in memory. The pre-streaming
// implementation used a `bytes.Buffer` that grew to the full PUT body
// size — for a multi-GiB chunked PUT on an 8 GiB Mac, that consumed
// the very memory webdavfs needed for its own buffer, which made the
// writeseq cap fire earlier and lengthened the PUT-response delay
// (Close had to do a multi-second io.Copy to flush the buffer to
// disk before returning). Streaming flips both: bridge memory
// footprint stays at one write buffer's worth, and Close returns in
// milliseconds because the data is already on disk.
type mtpNewFile struct {
	session      *mtp.Session
	store        *resume.Store // nil = legacy "refuse on truncation" behavior
	path         string
	expectedSize int64
	existingID   *uint32

	// Lazily created on first Write(). On Close, the path either
	// becomes the partial of a resume.Store session (truncation) or
	// is opened-and-streamed into MTP (success), then removed.
	tempPath     string
	tempFile     *os.File
	bytesWritten int64
}

func (f *mtpNewFile) ensureStaging() error {
	if f.tempFile != nil {
		return nil
	}
	var (
		path string
		file *os.File
		err  error
	)
	if f.store != nil {
		path, file, err = f.store.MakeStagingFile()
	} else {
		// Bare `make dev` mode (no resume store). Use system /tmp;
		// non-truncated commits will read+stream from here, then
		// delete on success.
		file, err = os.CreateTemp("", "comprador-staging-*.tmp")
		if err == nil {
			path = file.Name()
		}
	}
	if err != nil {
		return fmt.Errorf("create staging: %w", err)
	}
	f.tempPath = path
	f.tempFile = file
	return nil
}

// companionRegistered reports whether the Comprador menu-bar app is
// alive and polling. The store records a "last seen" timestamp on
// every /_comprador/* HTTP hit; we consider the companion present if
// that timestamp is within store.CompanionWindow. Without a present
// companion, the truncation path returns -36 (honest, matches the
// merged PR behavior) — lying with 200 OK would silently strand the
// file with no one to complete it.
func (f *mtpNewFile) companionRegistered() bool {
	if f.store == nil {
		return false
	}
	return f.store.IsCompanionActive()
}

func (f *mtpNewFile) Close() error {
	// Flush the staging file's buffered writes to disk so subsequent
	// reads (either MTP commit, or the companion via /append) see a
	// stable view. tempFile may be nil if no Write ever fired (zero-byte
	// placeholder PUT), in which case there's nothing to close and the
	// commit path falls through to a zero-byte SendFile.
	if f.tempFile != nil {
		if err := f.tempFile.Close(); err != nil {
			os.Remove(f.tempPath)
			f.tempFile = nil
			f.tempPath = ""
			return fmt.Errorf("close staging: %w", err)
		}
		f.tempFile = nil
	}

	// Truncation handling — Apple WebDAVFS writeseq mode silently caps
	// chunked PUT bodies at a memory-pressure-dependent threshold (32
	// MiB to several GiB observed). X-Expected-Entity-Length tells us
	// what the body should have been.
	//
	// With a resume.Store wired in (the default in production), we
	// hand the already-streamed staging file to the store as a session
	// (constant-time rename + JSON sidecar write — no io.Copy of the
	// body) and return 200 OK so webdavfs sees the PUT succeed quickly.
	// The Comprador menu-bar app then drives a side-channel completion
	// via /_comprador/sessions/*. See docs/RESUMABLE-UPLOADS.md.
	//
	// Without a store (bare `make dev`) or without an active companion,
	// we fall back to refusing the upload — the existing file is
	// preserved, the user sees a -36, no data loss.
	if f.expectedSize > 0 && f.bytesWritten < f.expectedSize {
		// Mode B handling: the body PUT can fail before delivering a
		// single byte (webdavfs's writeseq path can hit EADDRNOTAVAIL
		// on connectx — see MISTAKES.md 11d-bis). When that happens
		// `bytesWritten == 0` and `tempPath == ""` because Write
		// never fired. We still want to hand the companion something
		// to resume from — empty partial, full source upload from
		// offset 0. Create the staging file now so AdoptPartial has
		// a real path to rename.
		if f.bytesWritten == 0 && f.tempPath == "" && f.store != nil {
			if err := f.ensureStaging(); err != nil {
				log.Printf("Mode B persist: ensureStaging failed for %s: %v", f.path, err)
			} else {
				if err := f.tempFile.Close(); err != nil {
					log.Printf("Mode B persist: close empty staging for %s: %v", f.path, err)
				}
				f.tempFile = nil
			}
		}

		var stored *resume.SessionMeta
		if f.store != nil && f.tempPath != "" {
			meta, err := f.store.AdoptPartial(f.path, f.expectedSize, f.tempPath, f.bytesWritten)
			if err != nil {
				log.Printf("ADOPT FAILED for truncated %s (%d/%d): %v",
					f.path, f.bytesWritten, f.expectedSize, err)
				os.Remove(f.tempPath)
			} else {
				stored = meta
				f.tempPath = "" // ownership transferred to the store
				log.Printf("STRANDED %s: persisted %d/%d bytes as session %s",
					f.path, meta.ReceivedSize, meta.ExpectedSize, meta.ID)
			}
		} else if f.tempPath != "" {
			os.Remove(f.tempPath)
			f.tempPath = ""
		}

		if stored != nil && f.companionRegistered() {
			return nil
		}
		log.Printf("REFUSING truncated upload of %s: got %d bytes, expected %d (Apple WebDAVFS chunked-upload cap; existing file preserved)",
			f.path, f.bytesWritten, f.expectedSize)
		return fmt.Errorf("upload truncated by client: %d/%d bytes", f.bytesWritten, f.expectedSize)
	}

	// Non-truncated commit. The body is on disk at f.tempPath; open it
	// and stream into MTP.
	parent := path.Dir(f.path)
	base := path.Base(f.path)

	parentMeta, ok := f.session.Objects.GetByPath(parent)
	if !ok {
		if f.tempPath != "" {
			os.Remove(f.tempPath)
			f.tempPath = ""
		}
		return os.ErrNotExist
	}

	// Replace flow: delete the existing object now that the upload is
	// known to be complete. Done before SendFile so we don't briefly
	// have two objects with the same name in the same parent.
	if f.existingID != nil {
		delResp := f.session.Do(mtp.MTPRequest{
			Op:       mtp.OpDelete,
			ObjectID: *f.existingID,
		})
		if delResp.Err != nil {
			if f.tempPath != "" {
				os.Remove(f.tempPath)
				f.tempPath = ""
			}
			return delResp.Err
		}
		f.session.Objects.Remove(f.path)
		// Don't InvalidateDir; the Put below will refresh this entry,
		// and the parent's other children remain valid in the cache.
	}

	var bodyReader io.Reader = bytes.NewReader(nil)
	if f.tempPath != "" {
		body, err := os.Open(f.tempPath)
		if err != nil {
			return fmt.Errorf("open staging for MTP send: %w", err)
		}
		fcntlNoCache(body)
		defer func() {
			body.Close()
			os.Remove(f.tempPath)
		}()
		bodyReader = body
	}

	resp := f.session.Do(mtp.MTPRequest{
		Op:        mtp.OpSendFile,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      base,
		Size:      uint64(f.bytesWritten),
		Reader:    bodyReader,
	})
	if resp.Err != nil {
		return resp.Err
	}

	f.session.Objects.Put(&mtp.ObjectMeta{
		ID:        resp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      base,
		Path:      f.path,
		Size:      uint64(f.bytesWritten),
		ModTime:   time.Now(),
		IsDir:     false,
	})
	// Skip InvalidateDir(parent). The Put above is sufficient to keep
	// the cache correct: OpenFile called EnsurePopulated(parent) before
	// this upload started, so the parent's children list was already
	// complete; the new entry slots into that view. Invalidating here
	// would force the next PROPFIND to re-enumerate through the
	// session goroutine, which queues behind any in-flight GET (e.g.,
	// Finder QuickLook on the file we just uploaded). With the Put-
	// only path, PROPFIND serves from the cache and Finder sees the
	// fresh file immediately.
	return nil
}

func (f *mtpNewFile) Read(_ []byte) (int, error)          { return 0, os.ErrInvalid }
func (f *mtpNewFile) Seek(_ int64, _ int) (int64, error)  { return 0, nil }
func (f *mtpNewFile) Stat() (os.FileInfo, error) {
	return &mtpFileInfo{
		name:    path.Base(f.path),
		size:    f.bytesWritten,
		modTime: time.Now(),
		isDir:   false,
	}, nil
}
func (f *mtpNewFile) Readdir(_ int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }
func (f *mtpNewFile) Write(p []byte) (int, error) {
	if err := f.ensureStaging(); err != nil {
		return 0, err
	}
	n, err := f.tempFile.Write(p)
	f.bytesWritten += int64(n)
	return n, err
}

// mtpFileInfo implements os.FileInfo.
type mtpFileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

func (fi *mtpFileInfo) Name() string      { return fi.name }
func (fi *mtpFileInfo) Size() int64       { return fi.size }
func (fi *mtpFileInfo) Mode() fs.FileMode {
	if fi.isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (fi *mtpFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *mtpFileInfo) IsDir() bool        { return fi.isDir }
func (fi *mtpFileInfo) Sys() interface{}   { return nil }

func metaToFileInfo(m *mtp.ObjectMeta) *mtpFileInfo {
	return &mtpFileInfo{
		name:    path.Base(m.Path),
		size:    int64(m.Size),
		modTime: m.ModTime,
		isDir:   m.IsDir,
	}
}

func cleanPath(p string) string {
	p = path.Clean(p)
	if p == "." {
		return "/"
	}
	return p
}
