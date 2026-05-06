# Resumable Uploads — Architecture

> Status: design 2026-05-06, not yet implemented.
> Decision rationale lives in [`TODO.md`](../TODO.md).

## Problem in one paragraph

Apple WebDAVFS truncates chunked PUT bodies at a memory-pressure-dependent
cap (observed at ~4 GiB on a fresh Mac, but variable). The merged truncation
guard correctly refuses to commit corrupt half-uploads, but the cost is a
Finder error -36 dialog every time the user drags a large file. The bridge
sits at the HTTP server end of webdavfs and cannot influence the
kernel-side decision to enter the writeseq path; once webdavfs gives up
mid-PUT, no further bytes arrive over the wire.

The escape hatch is that the **source file is still on the Mac** at a
known filesystem path, and the bridge runs on the same machine as both
Finder and webdavfs. If we can discover the source file's path from the
incoming PUT request, we can read it directly — bypassing webdavfs's
truncation entirely.

## End-user experience target

Drag any-size file from Finder → file appears on phone. No dialog, no
retry, no menu items, no settings. Failure mode: if source can't be
found, surface a banner explaining the issue. Never present silent
data loss.

## Three actors

```
┌──────────┐                   ┌──────────────┐                   ┌─────────────┐
│  Finder  │ ── chunked PUT ─→ │   bridge     │ ←── XPC notify ── │  Comprador  │
│ (drag)   │                   │   (Go)       │                    │   (Swift)   │
└──────────┘                   └──────┬───────┘                    └──────┬──────┘
                                      │                                    │
                              MTP / libmtp                          NSMetadataQuery
                                      ↓                              + read(2)
                                  Phone                                    │
                                      ↑                                    │
                                      └────── side-channel HTTP ───────────┘
                                              POST /_comprador/resume
```

## Sequence — happy path

1. User drags `Attenborough.mkv` (8.47 GiB) onto `/Volumes/XQ-BT52.local`.
2. webdavfs starts a chunked PUT to the bridge with `X-Expected-Entity-Length: 9094280972`.
3. webdavfs's writeseq cap fires; client sends ~4.0 GiB of body and gives up.
4. Bridge's `mtpNewFile.Close` sees `received < expected`. **Instead of
   refusing**, it now:
   - Generates a session ID (`<uuid>`).
   - Persists the partial buffer to
     `~/Library/Application Support/Comprador/pending/<uuid>.partial`.
   - Writes a sidecar JSON
     `~/Library/Application Support/Comprador/pending/<uuid>.json` with
     `{path, expected_size, received_size, content_type, started_at, source_filename}`.
   - Notifies the Swift app over a Unix-domain socket
     (`~/Library/Application Support/Comprador/companion.sock`).
   - Returns **200 OK** to webdavfs (white lie — the upload is in
     progress, just not yet committed to MTP).
5. Swift app receives the IPC notification. It launches an
   `NSMetadataQuery` scoped to user-visible volumes
   (`NSMetadataQueryLocalComputerScope`) with predicate:
   ```
   kMDItemFSName == "Attenborough.mkv" AND kMDItemFSSize == 9094280972
   ```
6. **If exactly one match:** Swift opens the file with `read(2)` at offset
   `received_size`, streams the remainder to the bridge:
   ```
   POST /_comprador/resume?session=<uuid>
   Content-Length: 4798641932
   <remainder bytes>
   ```
7. Bridge's `/resume` handler appends the streamed body to
   `<uuid>.partial`. When total length reaches `expected_size`, it
   commits to MTP via the existing `SendFile` path, deletes the partial
   and sidecar, and clears the session.
8. File appears on phone. No dialog ever shown.

## Failure modes

### A. Zero matches in NSMetadataQuery
Source file isn't indexed (Spotlight excluded, removable volume, recent
download not yet processed). Swift app posts a UNUserNotification:
> **Comprador couldn't auto-complete Attenborough.mkv.** The original
> file isn't in Spotlight's index. Click here to choose it manually.
Click → standard NSOpenPanel → user picks file → resume flow continues
from step 6.

### B. Multiple matches in NSMetadataQuery
Two or more files match name + size (e.g., user has the same movie in
Movies/ and Downloads/). Swift app posts a chooser notification:
> **Comprador found two possible sources for Attenborough.mkv.** Click
> to pick the right one.
We do NOT silently pick one. The risk of grabbing the wrong file is
worse than asking.

### C. Resume HTTP request fails mid-stream
Network blip on loopback (rare), Swift app crashes, etc. Bridge waits
on `/resume` body with a 5-minute idle timeout. If timeout fires,
bridge re-notifies Swift app to retry. After 3 retries, bridge keeps
the partial on disk and surfaces a notification: "Upload of
Attenborough.mkv is stalled. Click to retry or discard."

### D. Source file size has changed since the PUT started
The Mac mtime/size sanity check in step 5 catches this. If the source
file's current size doesn't match `expected_size`, treat it as a
no-match and fall back to the chooser flow.

### E. User initiated multiple drags of the same filename
Each drag gets its own session ID. Sessions are independent. The
`expected_size` discriminator ensures we don't merge bytes from
different versions.

### F. Bridge crashes between truncation and resume
On startup, bridge enumerates `~/Library/Application Support/Comprador/pending/`,
loads sidecars, and notifies the Swift app to retry each session. If
the Swift app isn't running yet, the bridge holds them; once Swift app
starts, it pulls the queue.

## What we're betting on

**The heuristic.** `(filename, exact-byte-size)` is highly specific.
Movie/photo/document filenames are usually unique within a user's
filesystem; same filename with same exact byte size is extremely rare.
But it's a heuristic, not a guarantee. The chooser fallback (failure
mode B) is the safety net.

**Spotlight coverage.** The default `NSMetadataQueryLocalComputerScope`
covers `~`, `/Applications`, mounted volumes the user has visited, and
network shares Finder has indexed. Edge cases: files in
`/Volumes/some-untitled-USB`, files in directories the user excluded
from Spotlight via System Settings → Siri & Spotlight → Privacy.
Failure mode A (chooser) handles these.

**Filesystem permissions.** The Comprador menu bar app needs read access
to the user's source file. The app already requests Full Disk Access on
first launch for the device-claim flow; this extends that envelope.
Without FDA, the open() in step 6 fails with EPERM and we fall back to
the chooser.

## What we're explicitly NOT doing

- **Not** modifying webdavfs or its writeseq logic. Out of our control.
- **Not** parsing webdavfs's local cache files (unlinked anonymous
  tempfiles, inaccessible cross-process on macOS).
- **Not** asking the user to manually choose a source on every drag.
  The chooser is for the rare ambiguity case.
- **Not** advertising the side-channel `/resume` endpoint as a public
  API. It's an internal contract between bridge and Swift app.
- **Not** trying to handle uploads where the source has been deleted
  before resume completes. Failure mode D + chooser fallback.

## Out of scope (separate work)

- Resuming a webdavfs *download* (i.e., reading from the phone in
  Finder). Reads through the bridge already work end-to-end without
  truncation; the writeseq cap is upload-specific.
- Pure `cp` from Terminal users — `cp` doesn't enter writeseq, so it
  works at any size already. This whole architecture is for the
  Finder-drag user experience.

## Implementation phases

1. **Bridge: persist partial + sidecar + return 200.** Replace
   `mtpNewFile.Close`'s refuse-on-truncation logic with persist+notify.
   Roundtrip-testable with `curl` simulating a truncated PUT.
2. **Bridge: `/_comprador/resume` endpoint.** Accepts session-keyed
   continuation bodies. Handles concurrent appends, length validation,
   commit-on-complete.
3. **Bridge: crash-recovery on startup.** Re-emit pending notifications
   from disk-state.
4. **Swift: Unix-domain socket listener** + IPC protocol with bridge.
5. **Swift: NSMetadataQuery source-discovery** with the (name, size)
   predicate; one-match → resume, zero/multi → notification.
6. **Swift: `read(2)` source + side-channel POST** to bridge.
7. **Swift: chooser fallback** via NSOpenPanel for failure modes A and B.

Phases 1-3 are pure-bridge work and testable in isolation. Phases 4-7
are Swift work that depends on phase 1 being landed.
