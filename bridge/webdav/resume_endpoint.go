package webdav

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"

	"comprador/bridge/mtp"
	"comprador/bridge/resume"
)

// resumeEndpoint owns the /_comprador/* HTTP routes used by the
// Swift companion to drive resumable uploads.
//
// Path layout:
//
//	GET  /_comprador/sessions               list pending sessions (JSON)
//	GET  /_comprador/sessions/<id>          one session's metadata (JSON)
//	POST /_comprador/sessions/<id>/append   append body bytes; if total now
//	                                        equals expected, commits to MTP
//	POST /_comprador/sessions/<id>/discard  drop the session, free the disk
//
// The endpoints are deliberately under a path prefix that webdavfs has
// no reason to probe; they're not advertised in PROPFIND or OPTIONS,
// and they don't appear in the Finder volume.
type resumeEndpoint struct {
	store   *resume.Store
	session *mtp.Session
}

func (re *resumeEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Any traffic on /_comprador/* is proof the menu-bar app is alive
	// and polling, which gates the truncation 200-OK behavior. The
	// listing endpoint is the one the companion polls every few
	// seconds, but ping all of them so single-shot calls (manual curl,
	// debug endpoints) keep the gate open too.
	re.store.RecordCompanionPing()

	p := r.URL.Path
	switch {
	case p == "/_comprador/sessions" && r.Method == http.MethodGet:
		re.listSessions(w, r)
	case len(p) > len("/_comprador/sessions/") && p[:len("/_comprador/sessions/")] == "/_comprador/sessions/":
		re.sessionRoute(w, r, p[len("/_comprador/sessions/"):])
	default:
		http.NotFound(w, r)
	}
}

func (re *resumeEndpoint) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions := re.store.List()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(sessions); err != nil {
		log.Printf("resume: list encode: %v", err)
	}
}

// sessionRoute handles paths after `/_comprador/sessions/` — either
// "<id>" or "<id>/<verb>".
func (re *resumeEndpoint) sessionRoute(w http.ResponseWriter, r *http.Request, tail string) {
	id := tail
	verb := ""
	for i := 0; i < len(tail); i++ {
		if tail[i] == '/' {
			id = tail[:i]
			verb = tail[i+1:]
			break
		}
	}

	switch {
	case verb == "" && r.Method == http.MethodGet:
		re.getSession(w, id)
	case verb == "append" && r.Method == http.MethodPost:
		re.appendSession(w, r, id)
	case verb == "discard" && r.Method == http.MethodPost:
		re.discardSession(w, id)
	default:
		http.NotFound(w, r)
	}
}

func (re *resumeEndpoint) getSession(w http.ResponseWriter, id string) {
	meta, ok := re.store.Get(id)
	if !ok {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

func (re *resumeEndpoint) appendSession(w http.ResponseWriter, r *http.Request, id string) {
	meta, ok := re.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// The companion may include an `?offset=N` to assert it's only
	// streaming bytes from position N forward. We don't trust the
	// offset for correctness — we just check it matches what the
	// server thinks ReceivedSize is. Mismatch means the companion is
	// out of sync (e.g., another resume request landed between its
	// session-fetch and append).
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil {
			http.Error(w, "bad offset", http.StatusBadRequest)
			return
		}
		if offset != meta.ReceivedSize {
			http.Error(w, fmt.Sprintf("offset mismatch: want %d, got %d", meta.ReceivedSize, offset), http.StatusConflict)
			return
		}
	}

	defer r.Body.Close()
	total, err := re.store.Append(id, r.Body)
	if err != nil {
		log.Printf("resume: append %s: %v", id, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-fetch meta in case Append updated it.
	meta, _ = re.store.Get(id)

	if total >= meta.ExpectedSize {
		// Complete. Stream the assembled file into MTP.
		if err := re.commit(meta); err != nil {
			log.Printf("resume: commit %s: %v", id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"received_size": total,
		"expected_size": meta.ExpectedSize,
		"complete":      total >= meta.ExpectedSize,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (re *resumeEndpoint) discardSession(w http.ResponseWriter, id string) {
	if err := re.store.Cleanup(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// commit reads the assembled .partial file and SendFiles it to MTP,
// then cleans up the session. Mirrors mtpNewFile.Close's MTP path —
// kept separate to avoid awkward sharing of state between the two
// callers.
func (re *resumeEndpoint) commit(meta *resume.SessionMeta) error {
	parent := path.Dir(meta.Path)
	base := path.Base(meta.Path)

	// The original truncated PUT may have happened many minutes ago, and
	// the bridge may have been restarted in between (with the partial
	// recovered from disk). The object map is built lazily as Finder
	// browses, so the parent directory might not be in it. Walk from
	// root, populating ancestors, so the lookup below succeeds even on
	// a freshly-started bridge.
	re.session.EnsureInMap(parent)
	parentMeta, ok := re.session.Objects.GetByPath(parent)
	if !ok {
		return fmt.Errorf("commit: parent %s not in object map after populate walk", parent)
	}

	// Replace flow: if a file already exists at meta.Path, delete it
	// before sending the new one. We deferred this from the original
	// PUT specifically so a truncation wouldn't destroy the previous
	// version; now that the upload is whole, the swap is safe.
	if existing, ok := re.session.Objects.GetByPath(meta.Path); ok && !existing.IsDir {
		delResp := re.session.Do(mtp.MTPRequest{
			Op:       mtp.OpDelete,
			ObjectID: existing.ID,
		})
		if delResp.Err != nil {
			return fmt.Errorf("commit: delete existing %s: %w", meta.Path, delResp.Err)
		}
		re.session.Objects.Remove(meta.Path)
		re.session.Objects.InvalidateDir(parent)
	}

	// Hash the assembled partial before MTP sees it. Logged for diagnostic
	// receipts: compare against an md5 of the source on the originating Mac
	// to verify the assembled bytes are correct, *without* having to read
	// the file back from the phone (which costs a 5-10 minute MTP round-trip
	// per multi-GiB file). PTP has CRCs over USB, so md5(partial) ==
	// md5(source) is sufficient evidence that md5(phone) == md5(source) too.
	// Future: phone-side hashing for a true round-trip — see TODO.md
	// "phone-side checksum verification" entry.
	digest, hashErr := md5File(re.store.PartialPath(meta.ID))
	if hashErr != nil {
		log.Printf("resume: md5 of partial %s failed: %v (continuing with commit)", meta.ID, hashErr)
	}

	body, err := os.Open(re.store.PartialPath(meta.ID))
	if err != nil {
		return fmt.Errorf("commit: open partial: %w", err)
	}
	defer body.Close()

	if digest != "" {
		log.Printf("resume: about to commit %s (%d bytes, md5=%s)", meta.Path, meta.ExpectedSize, digest)
	}

	resp := re.session.Do(mtp.MTPRequest{
		Op:        mtp.OpSendFile,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      base,
		Size:      uint64(meta.ExpectedSize),
		Reader:    body,
	})
	if resp.Err != nil {
		return fmt.Errorf("commit: SendFile: %w", resp.Err)
	}

	re.session.Objects.Put(&mtp.ObjectMeta{
		ID:        resp.ObjectID,
		ParentID:  parentMeta.ID,
		StorageID: parentMeta.StorageID,
		Name:      base,
		Path:      meta.Path,
		Size:      uint64(meta.ExpectedSize),
		ModTime:   time.Now(),
		IsDir:     false,
	})
	re.session.Objects.InvalidateDir(parent)

	if err := re.store.Cleanup(meta.ID); err != nil {
		log.Printf("resume: cleanup %s: %v", meta.ID, err)
		// Don't fail the upload commit; it succeeded. Cleanup is
		// best-effort — orphan files will be reaped on next startup.
	}
	if digest != "" {
		log.Printf("resume: committed %s to MTP (%d bytes, md5=%s)", meta.Path, meta.ExpectedSize, digest)
	} else {
		log.Printf("resume: committed %s to MTP (%d bytes)", meta.Path, meta.ExpectedSize)
	}
	return nil
}

// md5File returns the lowercase hex md5 digest of the file at path. Used
// only for diagnostic logging in the resume commit path — never as an
// integrity gate, since md5 is broken for adversarial use and the
// transports we depend on (Apple WebDAVFS, libmtp/PTP, USB) carry their
// own integrity checks. md5 is fine for "did the bytes I assembled
// equal the bytes on the source Mac" proof, which is all we need.
func md5File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
