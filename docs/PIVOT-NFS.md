# Pivot: replace WebDAV with a localhost NFSv3 server

**From:** Claude Mercer (session of 2026-05-07)
**To:** Whoever picks this up next — future-Mercer, Codex, or a fresh hand
**Subject:** Architectural plan to escape the macOS WebDAV-on-Mac dilemma
by replacing the bridge's presentation layer with a Go NFSv3 server.

> **Status — 2026-05-07:** plan only. No code written. The leading
> candidate library (`willscott/go-nfs`) is cloned at
> `~/Labs/go-nfs` along with two fallbacks (`~/Labs/libnfs-go` for
> NFSv4 fallback, `~/Labs/go-smb` which turned out to be a client
> library and is out of consideration). The branch
> `claude/clever-blackwell-9c1b0f` contains the menu-bar progress UI
> for the unavoidable wait under the current WebDAV path; that ships
> as the right answer for the WebDAV path. This pivot is the way out
> of needing that progress UI at all.

---

## The problem in one paragraph

Returning quota properties (`quota-available-bytes` / `quota-used-bytes`
or the deprecated `quota` / `quotaused`) in the response to macOS's
mount-time PROPFIND triggers a hardcoded ~90 second wait inside the
macOS WebDAV mount machinery (verified empirically against macOS 15.4 +
XQ-BT52, 2026-05-07). Suppressing quota dodges the wait but breaks
Finder uploads with error 100060 (kPOSIXErrorENOSPC, reading from
cached `f_bavail == 0`). The chokepoint is below the NetFS layer:
mounting via AppleScript `mount volume` shares it (93 seconds in the
same test). Anything that produces a `webdav` mount type goes through
it. The only known architectural escape is to leave the WebDAV mount
type entirely.

For the empirical receipts and the hypotheses that were tested and
falsified, see [memory/ux_unavoidable_wait.md](../../../.claude/projects/-Users-terrace-Labs-Comprador/memory/ux_unavoidable_wait.md)
and sections 7–8 of [memory/mac_webdav_mtp_findings.md](../../../.claude/projects/-Users-terrace-Labs-Comprador/memory/mac_webdav_mtp_findings.md).

## The plan in one paragraph

Replace `bridge/webdav/` with `bridge/nfs/`. Use `willscott/go-nfs`
(Apache 2.0, pure Go, NFSv3) as the server library. Wrap our existing
`mtp.Session` + `ObjectMap` in a `go-billy.Filesystem` adapter and
hand it to go-nfs. Listen on a random localhost TCP port. Replace
`MountManager.swift`'s `NetFSMountURLSync` call with an RPC to the
existing `comprador-helper` SMAppService.daemon, which invokes
`mount_nfs -o port=N,mountport=N,nfsvers=3,nolocks,tcp localhost:/ /Volumes/<name>`.
The MTP layer doesn't change at all; the phone never knows we swapped
presentation layers.

## Architecture

### Before (current)

```
Comprador.app menu bar (Swift)
  ↓ MountManager.swift
NetFSMountURLSync (NetFS framework)
  ↓
mount_webdav (system) + WebDAVFS kext
  ↓ HTTP loopback
bridge/webdav/        ← presentation layer (golang.org/x/net/webdav)
bridge/resume/        ← workarounds for WebDAVFS writeseq cap
bridge/mtp/           ← libmtp + ObjectMap + Session goroutine
  ↓ libmtp + libusb
phone (MTP over USB)
```

### After (proposed)

```
Comprador.app menu bar (Swift)
  ↓ MountManager.swift
HelperClient.mountNFS(port, name)  ← new
  ↓ Unix socket RPC
comprador-helper (SMAppService.daemon)
  ↓ exec mount_nfs(8)
macOS NFS client (built-in)
  ↓ NFSv3 over loopback TCP
bridge/nfs/           ← new presentation layer (willscott/go-nfs)
bridge/mtp/           ← unchanged
  ↓ libmtp + libusb
phone (MTP over USB)
```

The MTP layer is unchanged. The Mac-side workaround layers (resumable
uploads, 102 Processing keepalives, Finder probe-file 404 handling,
the `noopLockSystem`) all disappear with WebDAV.

## Concrete phases

### Phase 1: NFS-server stub against memfs (~half day)

Goal: prove the macOS NFS client speaks to a go-nfs-served filesystem
on the same machine, before involving any of our MTP code.

- New `bridge/nfs/server.go` that mirrors the structure of
  go-nfs's `example/osview/main.go`: TCP listen on `127.0.0.1:0`,
  print port to stdout, hand a memfs-backed billy.Filesystem to
  `nfs.Serve`.
- Build it as a separate binary or as a `--mode nfs-stub` flag on the
  existing bridge binary; whichever is cheaper.
- Manually mount with `sudo mount -o port=N,mountport=N,nfsvers=3,nolocks,tcp -t nfs localhost:/ /Volumes/test-nfs`
  and verify Finder shows it without a 90s wait.
- Drag a file in and confirm Finder uploads work.

If this phase hits a wall, the entire pivot is uncertain and we should
fall back to a different protocol or library (see Fallbacks below).
This is the cheapest thing to try first.

### Phase 2: billy.Filesystem adapter for MTP (~2–3 days)

Goal: implement `mtpBillyFS` that translates `billy.Filesystem` and
`billy.File` calls into operations on the existing `mtp.Session`.

The billy interface is path-based, which suits our `ObjectMap`:

| billy method | MTP op | Notes |
|---|---|---|
| `Open(path)` | `OpGetFile` | Fetch to staging, return `*billy.File` over the staging fd. Same pattern as current `mtpFile`. |
| `OpenFile(path, flags, mode)` | `OpSendFile` (on close) | Stream writes to staging tempfile; commit on close. Same pattern as `mtpNewFile`. |
| `Stat(path)` | `ObjectMap.GetByPath` | No round-trip; cache lookup. |
| `Rename(old, new)` | Get + Send + Delete | Same as today; libmtp has no native rename. |
| `Remove(path)` | `OpDelete` | |
| `MkdirAll(path)` | `OpCreateFolder` (recursive) | |
| `ReadDir(path)` | `Session.EnsurePopulated` + `ObjectMap.ListChildren` | |
| `Symlink/Readlink/Lstat` | ENOTSUP | MTP has no symlink concept. |

The `Statfs` call (NFS `FSSTAT` reply) populates from
`Session.TotalBytes()` / `FreeBytes()` directly. No PROPFIND middleman,
no quota-property dance, no 90s wait.

go-nfs's `Handler` interface also requires `ToHandle` / `FromHandle`
(stable file-handle generation); the `nfshelper.NewCachingHandler`
default is suitable as a starting point.

### Phase 3: privileged mount via helper (~1 day)

`mount_nfs` is not setuid on macOS (`-rwxr-xr-x  root  wheel`).
Mounting requires root. The existing `comprador-helper`
(SMAppService.daemon) has the privilege; extend its protocol.

Current helper protocol commands (from `helper/main.go`):
`ADD <name>`, `REMOVE <name>`, `CLEAR` for `/etc/hosts` management.

Add:
- `MOUNT_NFS <port> <volume-name>` → exec `mount_nfs ...`, return success/error
- `UNMOUNT_NFS <volume-name>` → exec `umount` or `diskutil unmount`

Replace `MountManager.swift`'s `NetFSMountURLSync` call with a
`HelperClient.mountNFS(port:, name:)` RPC. Unmount path stays via
`DiskArbitration` (works for any mount type) or routes through the
helper symmetrically.

The mount options that matter (start with these, tune empirically):

```
nfsvers=3       # the version go-nfs implements
tcp             # macOS NFS over UDP has its own quirks
nolocks         # NLM is more trouble than it's worth on a USB-attached device
port=N          # the bridge's chosen port (no privileged port 2049 collision)
mountport=N     # same; go-nfs serves both protocols on the same listener
```

Avoid `noac` (no attribute caching) — it pushes per-stat traffic
through the bridge which is wasteful. Default attribute cache is fine
for an MTP-backed FS where mutations are mostly bridge-driven.

### Phase 4: deletion sweep (~half day)

Once the NFS path is proved with real MTP, delete the WebDAV-specific
infrastructure that exists *only* because of WebDAVFS quirks:

- `bridge/webdav/` — entire package
- `bridge/resume/` — entire package (writeseq cap workaround)
- `MenuBarApp/Sources/ResumeCompanion.swift` — companion that drives
  the resume protocol from the Swift side
- The 102 Processing keepalive code path in `finderHandler.servePutWithKeepalive`
- The `X-Expected-Entity-Length` header handling
- `noopLockSystem` (WebDAV-specific)
- The Finder probe-file 404 handling (`isFinderProbe`) — NFS just
  returns ENOENT, which Finder handles natively
- The `_comprador/sessions/*` HTTP endpoints
- `docs/RESUMABLE-UPLOADS.md` (or update it with a note that the
  pivot rendered it historical)

Rough size estimate (today's branch): `bridge/webdav/handler.go`
alone is ~1000 LOC; `bridge/resume/` is ~500; ResumeCompanion is
~300. Net deletion is probably 1500–2500 LOC, partially offset by
~500–1000 LOC of new NFS code. The project is *smaller* after the
pivot, not bigger.

The menu-bar progress UI (the `connectStatus` / elapsed-counter /
hint plumbing in `AppDelegate.swift`) can stay or shrink. Without
the 90s NetFS wait, the connecting phase compresses to a few seconds
and the progress UI becomes vestigial. Probably keep the status
text (it's useful) and remove the elapsed counter and hint.

### Phase 5: stress test

The same regression battery the WebDAV path uses today, plus:

- Drag Shrek (1.5 GB-ish) — confirm landing
- Drag Attenborough (9 GB) — this is the writeseq-cap stress file;
  on NFS it should just work without resumable-upload bookkeeping
- Open QuickLook on a multi-GiB file — confirm it cancels cleanly
  (NFS reads are range-based, libmtp's session is interruptable;
  this should be more responsive than WebDAV's "fetch the whole
  file before serving" pattern)
- Mid-transfer phone unplug — confirm Finder shows a sensible error
  rather than hanging. NFS is stricter about server liveness than
  HTTP; this is a known risk
- Eject from menu bar — confirm clean unmount

## Risks

- **go-nfs is "minimally tested" per its own README.** Expect to find
  bugs the maintainer hasn't seen. Apache 2.0 means we can fork.
- **The macOS NFS client has its own quirks** (caching, attribute
  timeouts, mount-option idiosyncrasies). Tunable but may take
  iteration.
- **Disconnect semantics may be stricter than HTTP.** When the phone
  unplugs mid-transfer, NFS clients are less forgiving. May surface
  as Finder hangs that the WebDAV path currently dodges.
- **Helper protocol expansion.** Adding mount/unmount commands to
  the helper requires careful argument validation — the helper runs
  as root and parses untrusted input.
- **Privilege approval surface unchanged.** The helper already needs
  user approval (Login Items toggle); we're not adding a new approval
  prompt, just extending what the existing helper does.

## Fallbacks

In order of preference if the primary plan hits a wall:

1. **`smallfz/libnfs-go` (NFSv4)** — same architectural shape, MIT,
   pure Go. Their README publishes test results showing real gaps in
   operations we'd need (`mkdir 5/7`, `unlink 2/4`, `link x`,
   `fcntl x`). Worse starting point than go-nfs but a real fallback.
   Cloned at `~/Labs/libnfs-go`.

2. **FUSE-T (bundled)** — Cryptomator's actual default mount path on
   modern macOS via `fuse-nio-adapter`. Userspace daemon, no kext.
   Costs: license verification, install-on-first-launch flow, second
   process to manage. Real but heavier than option 1.

3. **Pure-Go SMB server (`cloudsoda/go-smb2-server` or similar)** —
   different protocol entirely. macOS SMB client is mature and
   battle-tested by Apple. Risk: pure-Go SMB servers may not handle
   all of Apple's `smbfs` quirks the way Samba does. Worth surveying
   if NFS approach is killed by something specific to NFS.

4. **Bundled Samba `smbd`** — battle-tested but ~50–100 MB bundle
   addition, GPLv3 distribution obligations, daemon config heavy.
   Reject unless protocol-fidelity is the killing factor.

5. **Wait for Apple to fix the 15.4 regression.** Cryptomator's
   `OSUtil.isMacOS15_4orNewer` carve-out implies they expect this is a
   finite-lifespan regression. Could be 15.5; could be 17. Not a plan,
   just a possibility worth monitoring.

## What gets deleted

The Mac-side workarounds in the bridge that exist *only* because of
WebDAVFS-specific quirks:

| File / area | LOC (approx) | Reason for existence |
|---|---|---|
| `bridge/webdav/handler.go` | ~1000 | WebDAV protocol surface |
| `bridge/resume/` (entire package) | ~500 | writeseq-cap truncation persistence |
| `MenuBarApp/Sources/ResumeCompanion.swift` | ~300 | drives resume protocol from Swift side |
| `_comprador/sessions/*` HTTP endpoints | embedded in handler | sidechannel for resume |
| 102 Processing keepalive code | ~100 | PUT-response timeout dodge |
| `X-Expected-Entity-Length` handling | ~30 | writeseq-cap detection |
| Progress UI elapsed counter / hint | ~50 | unavoidable wait UX (becomes unnecessary) |

Net deletion ~2000 LOC; net addition (`bridge/nfs/`) probably
500–1000 LOC. The project shrinks.

What stays unchanged:

- `bridge/mtp/` — entire package (libmtp bindings, Session, ObjectMap)
- `helper/` — protocol gets extended, core stays
- `MenuBarApp/Sources/DeviceWatcher.swift`, `BridgeProcess.swift`,
  most of `AppDelegate.swift` — Mac-side lifecycle is unchanged
- `MenuBarApp/Sources/USBSeizer.swift` — IOKit USB seize is unchanged
- The mDNS hostname registration in the bridge — still useful
  for clean volume names

## First steps for next-Mercer

Day 1:

1. **Read this document.** Then read
   [memory/ux_unavoidable_wait.md](../../../.claude/projects/-Users-terrace-Labs-Comprador/memory/ux_unavoidable_wait.md)
   and section 7 of
   [memory/mac_webdav_mtp_findings.md](../../../.claude/projects/-Users-terrace-Labs-Comprador/memory/mac_webdav_mtp_findings.md).
   The empirical falsifications matter — don't re-walk them.

2. **Run the go-nfs `osview` example as-is** against a temp
   directory. Confirm `mount -t nfs ...` works on the current
   machine. If it doesn't, the entire plan is gated on figuring
   out why.

3. **Phase 1 stub.** Wire go-nfs into `bridge/nfs/server.go` as a
   separate command-line mode of the existing bridge, backed by an
   in-memory billy.Filesystem (memfs). Mount it manually, confirm
   speed and basic uploads.

4. **Don't touch the WebDAV code yet.** The current branch ships
   with working uploads and the 90s wait surfaced via UI. The pivot
   should land on a fresh branch off the same base, with the WebDAV
   path intact until the NFS path is proven end-to-end.

5. **Once Phase 1 works, schedule Phase 2 (billy adapter for MTP).**
   This is where the bulk of the engineering risk lives — making
   the path-based billy interface play nicely with the MTP object
   model. Budget more time than feels reasonable; libmtp's lack of
   true partial reads will surface differently here than under
   WebDAV.

## Reference material

- `~/Labs/go-nfs` — primary candidate (NFSv3, Apache 2.0)
- `~/Labs/libnfs-go` — fallback (NFSv4, MIT)
- `~/Labs/copyparty` — read commit 8e046fb6 to understand what their
  fix actually does (suppresses quota wholesale on Mac; their users
  upload via HTTP, not Finder, so they don't see the broken-uploads
  consequence we measured)
- `~/Labs/Cryptomator`, `~/Labs/webdav-nio-adapter`,
  `~/Labs/webdav-nio-adapter-servlet` — read
  `MacAppleScriptMounter.java` to confirm AppleScript path is *not*
  the trick (it shares the 90s wait); read `DavFolder.java`'s
  `OSUtil.isMacOS15_4orNewer` carve-out for the canonical user-side
  symptom report
- Apple kext source: `apple-oss-distributions/webdavfs` — useful for
  understanding what we're getting away from, but not required reading
  for the pivot

## Closing

The WebDAV path was the right initial choice — it shipped, it works,
and Comprador v0.2.3 is in users' hands today. The 90s wait wasn't
foreseeable from the project's design phase; macOS 15.4 introduced it.
This pivot doesn't say WebDAV was wrong; it says the platform changed
under our feet and we have a known path off it.

The MTP correctness work — object map invalidation policy, session
serialization, lazy enumeration, IOKit USB seize, the keepalive dance,
the resumable-upload bookkeeping in the *MTP* sense, the entire
phone-side model — all of it carries forward verbatim. We've spent
months getting that right; nothing in this pivot threatens any of it.
