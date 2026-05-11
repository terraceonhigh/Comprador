# Comprador — TODO

## ⚠ Roadmap imperative — cgo callback buffer reuse

**This is the single fix that gates Comprador's strategic
differentiator.** Treat it as a hard prerequisite, not a
"nice to have."

The cgo MTP callback path
([bridge/mtp/binding_callbacks.go](bridge/mtp/binding_callbacks.go))
calls `make([]byte, wantlen)` on every libmtp invocation. A 9 GiB
transfer generates ~400 allocations of ~22 MiB each; on macOS
`MADV_FREE` keeps them in the process's address space until
kernel reclaim. Detailed receipt at [MISTAKES.md entry 8a](docs/MISTAKES.md).

**Why this is now load-bearing, not just an open bug:**

[PLAN-MULTI-DEVICE.md](docs/PLAN-MULTI-DEVICE.md) commits us to
**true concurrent multi-device** — N phones plugged in, N Finder
sidebar entries, all browseable simultaneously. The forensics
revealed that **no other macOS MTP app does this:** OpenMTP
refuses multi-attached devices, SwiftMTP detects-many-but-mounts-one,
Image Capture isn't a filesystem, Android File Transfer was
single-device. Shipping this makes Comprador the only Mac app
that treats two phones as two filesystems concurrently. The moat
is the subprocess-per-bridge architecture we already paid for.

**But:** two devices plugged in means two bridges in memory.
Two simultaneous multi-GiB transfers means **18 GiB of leaked
`VM_ALLOCATE` regions** on an 8 GiB Mac. The system thrashes,
swaps, and either OOMs the bridges or kicks the user into
unbearable lag. That's not a degraded experience — it's an
unshippable one.

**Until this fix lands, multi-device cannot ship.** Not "shouldn't,"
not "would be nicer if." Cannot. The single most strategically
valuable feature on Comprador's roadmap is gated on ~30 lines of
Go. Hold a single `[]byte` buffer in the registry entry alongside
the `io.Reader`/`io.Writer`; reuse across callbacks; grow once if
a `wantlen` exceeds current capacity. Caps Go-side memory at one
chunk (~22 MiB) per concurrent MTP operation. Sister entry
preserved below in the High impact section for the technical
breakdown; this section is the imperative framing.

**Sequence consequence:** the cgo fix is the *first* item to land
before any multi-device implementation work begins. PLAN-MULTI-
DEVICE.md documents this as non-negotiable. Don't reorder.

---

## Architecture risk — partially mitigated, still open

- [ ] **Reconsider the architecture if RAM is a binding constraint.**

      **Original observation 2026-05-06 (pre-streaming-refactor):** on
      an 8 GiB-RAM Mac with ~67 MiB free, dragging a 9.09 GiB
      Attenborough.mkv via Finder: Finder's progress bar climbed
      silently while webdavfs accepted bytes it had nowhere to buffer;
      the bridge received zero body bytes for ~3 minutes; eventually
      webdavfs flushed ~4.3 GiB in a burst (truncated by the writeseq
      cap). Phase 2 auto-completion succeeded but the *journey* —
      opaque memory starvation, intermittent EADDRNOTAVAIL on the
      companion's URLSession, false-timeout in URLSession during the
      MTP commit window — was unacceptable for non-developer users.

      **What changed (commit 0c5a18e):** the bridge no longer buffers
      PUT bodies in a `bytes.Buffer`; it streams every Write directly
      to a staging file on disk, and opens that staging file with
      `F_NOCACHE` when reading it back for the MTP send. Go-side heap
      for the WebDAV layer dropped from "approximately the file size"
      to a handful of MiB regardless of upload size. webdavfs's
      writeseq cap stayed comfortably high on a fresh-boot 8 GiB Mac;
      the same 9.09 GiB Attenborough.mkv that previously hit Mode A
      went through in a single uninterrupted PUT.

      **What we then learned (re-verification 2026-05-06):** during
      the MTP `SendFile` *after* the PUT body has fully arrived, the
      bridge process still hits ~10 GB physical footprint. The hog
      isn't the page cache (F_NOCACHE worked) and isn't the WebDAV
      buffer (streaming worked) — it's the **cgo callback path**:
      `goDataGetFunc` (and `goDataPutFunc` on the GET side) calls
      `make([]byte, int(wantlen))` per invocation, generating roughly
      one Go heap allocation per libmtp chunk (~22 MiB each, ~400
      for a 9 GiB file). macOS's `MADV_FREE` policy keeps those
      allocations in the process's address space until kernel
      reclaim. Vmmap shows them as 409 `VM_ALLOCATE` regions. See
      MISTAKES.md entry 8a — the fix is reusing one buffer per
      session via the registry, ~30 lines of code, listed in High
      impact below.

      **Until that fix lands:** Mode A on 8 GiB Macs is no longer
      common because webdavfs now stays well-fed during the PUT;
      but every multi-GiB transfer still leaks ~9 GiB of process
      memory into swap until the bridge dies. The system thrashes
      but doesn't crash. **Don't click multi-GiB phone files in
      Finder while a transfer is recent** — see MISTAKES.md
      entry 11d-tris for why QuickLook is an attractive nuisance.

      **What's still open even after the cgo fix:** files larger
      than the headroom we'd then have (probably "most of available
      RAM") will still hit the writeseq cap. Phase 2 catches that
      case automatically, but the user-visible journey reverts to
      the pre-mitigation pattern: a -36 dialog while the companion
      silently completes the upload over ~7 minutes. That's the
      remaining bad UX cliff.

      Architectural options for closing the cliff entirely, in
      decreasing order of preserving the current Finder-volume model:

      1. **Drop webdavfs from the upload path** (read-only mount for
         browsing; uploads via a Finder Service or sidebar entry that
         hands the source path directly to the bridge, bypassing
         chunked PUT entirely). Eliminates the writeseq cap. Hardest
         architecture change, biggest UX win for very large files.
      2. **Sidebar-only Finder integration** (no mounted volume at
         all) — like Image Capture. Loses "browse the phone from
         Finder" but eliminates webdavfs entirely.
      3. **File Provider extension** — Apple's first-party FS
         extension API. Sandboxed, memory-managed by the system, but
         USB device access from a File Provider extension is deeply
         restricted. Investigated and rejected in `CLAUDE.md` ("Why
         not File Provider API?"); worth re-examining only if (1) and
         (2) turn out unworkable.

      Pre-1.0 for non-developer users: probably no longer urgent
      after the streaming refactor — the 8 GiB Mac case is genuinely
      shippable now. But "files >12 GiB on low-RAM Macs go through
      Mode A with a -36 flash" is a known cliff worth deciding about
      before any user-visible 1.0 marketing makes claims about
      arbitrary file sizes.

## High impact (correctness / UX friction)

- [ ] **cgo MTP callback: reuse buffer per session instead of
      allocating per call.** `bridge/mtp/binding_callbacks.go`'s
      `goDataGetFunc` and `goDataPutFunc` each call
      `make([]byte, int(wantlen))` on every invocation. For a 9 GiB
      transfer that's ~400 allocations of ~22 MiB each, all 9 GiB
      of which Go's runtime hands back to the OS via `MADV_FREE`
      but stays attributed to the process (visible as 409
      `VM_ALLOCATE` regions in `vmmap`) until kernel reclaim.
      On low-RAM Macs the OS pages it to swap and the system
      thrashes. Fix: hold a single `[]byte` buffer in the registry
      entry alongside the io.Reader/io.Writer; reuse across
      callbacks, grow once if a wantlen exceeds current capacity.
      Caps Go-side memory at one chunk (~22 MiB) per concurrent
      MTP operation. Receipt + analysis in MISTAKES.md entry 8a.
      After the fix, profile to confirm and to surface any
      remaining C-side libmtp allocations.

- [ ] **Make GETs cancellable (revisit longstanding TODO).** Tied
      to MISTAKES.md entries 11d (deadlock under read pressure)
      and 11d-tris (QuickLook hazard). When a Finder click on a
      large phone file triggers a multi-minute MTP read, dismissing
      the QuickLook should kill the in-flight read, not let it run
      to completion blocking every other operation. Implementation
      hooks: pass a `context.Context` from the HTTP handler down
      through the session goroutine; on context cancel, call
      `LIBMTP_Cancel_Operation` (if libmtp supports it for the
      relevant transaction type) or close the device-side USB
      transfer to make libmtp's read return.

## High impact (UX friction)

- [x] Volume shows as "127.0.0.1" in Finder sidebar — should show device name (e.g. "Pixel 6")
      Bridge now registers `<DeviceName>.local → 127.0.0.1` via mDNS using
      `dns-sd -P` and advertises the URL with that hostname. NetFS auto-names
      the volume from the URL host, so Finder now sees `/Volumes/Pixel-6.local`.
      Verified end-to-end with a stub WebDAV server.
- [x] Drop the `.local` suffix from the Finder sidebar entry (Pixel-6.local → Pixel-6)
      A privileged helper (LaunchDaemon registered via `SMAppService.daemon`)
      now manages a block in `/etc/hosts` so the bridge can advertise
      `http://Pixel-6:port/`. macOS prompts the user once to approve it in
      Login Items; afterwards every device gets a clean single-label name.
      Strict server-side validation (`^[A-Za-z][A-Za-z0-9-]{0,62}$`, no
      reserved labels) prevents impersonation of real domains. mDNS remains
      as a fallback when the helper isn't approved.
- [x] Device name shows "Android Device" — fall back to `LIBMTP_Get_Modelname` when friendly name is empty (already in `binding.go`)
- [x] Login item registration — offer to start at login on first launch via `SMAppService`

## Security cleanup — v0.4.0 priority

- [ ] **Remove the privileged helper (`comprador-helper`,
      `SMAppService.daemon`).** Per
      [CHANGELOG v0.3.0](CHANGELOG.md), the helper is no longer
      invoked on the NFS mount path — it remains bundled only
      for legacy WebDAV cosmetic features (hostname rewriting
      via `/etc/hosts` to drop the `.local` suffix). It is the
      **single largest privilege-escalation surface in the
      bundle** ([SECURITY.md](docs/SECURITY.md)). Removal blocks
      on retiring the WebDAV mount path entirely; bump it from
      "slated for v0.4.0" to "the v0.4.0 priority cleanup item."
      Helper code in [helper/](helper/), bundled in
      `$(SWIFT_APP)/Contents/MacOS/comprador-helper` per the
      `BUNDLE_HELPER` Makefile recipe.

- [ ] **Subscribe to upstream libmtp / libusb releases.** No
      formal cadence today. Manual check per Comprador release
      cycle: hit
      [libmtp upstream](https://sourceforge.net/projects/libmtp/files/libmtp/)
      and
      [libusb releases](https://github.com/libusb/libusb/releases)
      before tagging each v0.x.0; bump if a security fix has
      shipped. Captured in [SECURITY.md "Tracked items"](docs/SECURITY.md).

## Morning review — human-facing docs

- [ ] **Walk through human-facing docs in the morning** with fresh
      eyes after last night's substantial changes. The day generated
      a lot of new doc surface (SECURITY.md, PRE-LAUNCH.md,
      RESEARCH-IMAGECAPTURECORE.md, PLAN-MULTI-STORAGE.md,
      PLAN-MULTI-DEVICE.md, USER.md updates, OPENMTP/SWIFTMTP-NOTES
      updates, NOTICES.md "Logo" section, CLAUDE.md "Security
      Invariants" section) and substantial restructuring (sibling
      repos moved to references/, icon pared to one PNG, multi-device
      commitment now in the affirmative). Update as necessary:
      - **README.md** — does the headline still read right? Does
        "Single device at a time (today)" still match where we are?
        Is the architecture section consistent with the bridge/icon
        paring?
      - **CHANGELOG.md** — anything we want to write before the
        next tag?
      - **The forensics docs (OPENMTP/SWIFTMTP/COPYPARTY/CRYPTOMATOR/
        GO-NFS/LIBNFS-GO NOTES.md)** — references/ paths verified;
        any stale claims now that the architectural picture has
        shifted?
      - **USER.md** — does the "What we are building toward" section
        still match what we're committing to?
      - **PRE-LAUNCH.md** — the launch checklist; sanity-check the
        hard/soft/nice-to-have buckets.

      A morning pass with coffee will catch the inconsistencies a
      tired late-evening eye missed.

## Medium impact (reliability)

- [ ] Error recovery — detect bridge crash mid-session, auto-restart
- [ ] Handle detach during file transfer gracefully (don't hang Finder)
- [x] **Reattach-during-unmount race leaves Comprador in dead state.**
      Reproduced 2026-05-06: after a successful mount, a USB
      detach+reattach storm fires within milliseconds (typically
      phone-side: screen sleep, MTP layer flutter). The reattach
      handler runs while the corresponding unmount is still in flight,
      sees `isMounted == true`, logs `Ignoring attach — already
      mounted`, and discards the event. Unmount then completes, the
      bridge is gone, no further attach event ever arrives, and the
      menu bar app is stuck in detached-no-bridge state until the user
      physically replugs. Two-line fix sketched in
      [docs/MISTAKES.md](docs/MISTAKES.md) entry 19a: track
      `pendingUnmount` and queue the reattach for after, OR
      synthesise an attach at the end of every successful unmount if
      IOKit still sees the device. Latter is cleaner — it's a one-shot
      "did the bus settle in a state that needs us?" check at unmount
      completion.
- [ ] Session recovery — reopen MTP session on corruption without full bridge restart
- [ ] **Make Finder error -36 disappear for very large files.**
      **Decision 2026-05-06**: pursuing option C (bridge ↔ Swift companion
      + direct source-file read on truncation). Options A and B were
      considered and rejected: A leaves the error visible as a documented
      limitation; B still surfaces a modal -36 dialog plus an implicit
      "drag again" rule, which is user-visible complexity by the standard
      we set. C is the only path that makes the failure invisible. Logging
      the choice for posterity — the future will judge it as either
      brilliance or hubris, depending on whether the source-discovery
      heuristic holds up. See [docs/RESUMABLE-UPLOADS.md](docs/RESUMABLE-UPLOADS.md)
      for the architecture.
      Apple WebDAVFS's writeseq path truncates chunked PUT bodies at a
      memory-pressure-dependent cap (observed at ~4 GiB on a fresh Mac
      2026-05-05; could be tens of MiB under load). PR #2's truncation
      guard prevents corrupt half-uploads — that's correct — but the user
      still sees -36 if their file is bigger than today's cap. Three
      options worth trying, in order of cost:
      1. ~~**`MNT_SYNCHRONOUS` mount flag**~~ — confirmed dead 2026-05-05.
         Set `kNetFSMountFlagsKey = MNT_SYNCHRONOUS` on the NetFS mount
         call; the kernel mount syscall accepts it but webdavfs's
         `webdav_mount` filters it out before it reaches the VFS state.
         `statfs()` on the mounted volume reads `f_flags = 0x1c`
         (NOEXEC+NOSUID+NODEV only), no SYNCHRONOUS bit. Don't retry.
      2. **Bridge-side persistent partial buffering with auto-merge on
         retry.** When the truncation guard fires, persist the partial
         body to disk keyed by `(path, X-Expected-Entity-Length)`. On
         the next PUT to the same path with the same expected size,
         skip bytes already cached and append the new ones; commit to
         MTP once total length is reached. UX: one -36 dialog, then a
         second drag completes the upload. Zero menu items, zero
         settings, zero education. Two days of work, low risk.
      3. **Bridge ↔ Swift XPC companion + direct source read on
         truncation** (deferred). On `REFUSING truncated upload`, the
         bridge signals the Comprador app, which uses NSMetadataQuery
         to locate the source file by filename + recent mtime, opens
         it directly bypassing webdavfs, and streams the missing tail
         to `/_comprador/resume`. UX: no -36 at all, file appears on
         phone. Risk: source-match is heuristic and can grab a
         like-named file instead. Defer until #1 and #2 are exhausted.

      Shelved (UX complexity rejected 2026-05-05): right-click → "Send
      to phone…" Finder Service that bypasses webdavfs entirely by
      having the bridge open the source file path directly. Works
      reliably for any size, but requires the user to learn a
      non-standard interaction. Keep as escape-hatch idea if all three
      above fail to land cleanly.

## Testing infrastructure

- [ ] **Phone-side checksum verification.** Currently we md5 the bridge's
      assembled .partial file before MTP commit (see `resume.commit` in
      `bridge/webdav/resume_endpoint.go`) and compare to a Mac-side md5
      of the source. This catches assembly bugs (wrong byte offsets,
      truncated POSTs, etc.) without paying the 5-10 minute MTP read-back
      cost per multi-GiB file. PTP/USB carry CRCs, so md5(local-partial)
      == md5(source) is strong evidence that md5(phone) == md5(source).

      But it's not a *true* round-trip check. To catch the rare case
      where libmtp itself misbehaves (or the device-side filesystem
      corrupts on write — Android's FUSE-based MTP layer has had bugs
      historically), we'd want md5 computed *on the phone*.

      Options worth weighing if/when this becomes worth doing:

      1. **ADB shell `md5sum /sdcard/Download/<file>`.** Cheapest by far,
         single command. But ADB is explicitly out of scope for the
         shipping product (CLAUDE.md "Why not ADB?" — requires Developer
         Options + USB Debugging, which is the friction we're avoiding).
         For the test harness only, gating ADB usage behind a
         `COMPRADOR_TESTING_ADB=1` env var would be acceptable: we don't
         need the user to enable Developer Options to use Comprador, only
         the developer running tests.

      2. **MTP `LIBMTP_Get_File_To_Handler` with an md5-computing
         handler.** Bypasses Finder/webdavfs entirely on the read path;
         streams device → libmtp → md5. Same MTP throughput cost as the
         WebDAV round-trip, no improvement on the bottleneck. Only useful
         if we want to verify *MTP-readback* specifically rather than
         "what's stored on the device."

      3. **Companion phone app exposing a "hash this file" intent.** A
         tiny Android side-loadable that listens for an intent and
         returns md5. Would also avoid Developer Options. Heavy lift for
         a testing convenience.

      Recommendation: ship option 1 as a `make test-md5` target
      (developer-side only, never bundled into the user-facing app)
      whenever we next have a reason to suspect MTP write integrity. For
      now, the bridge-side md5-on-commit log is sufficient.

## Low impact (completeness)

- [ ] Multiple storage support (phones with SD cards → subdirectories under single mount)
- [x] Notarization build configuration (hardened runtime + signing for distribution)
- [ ] Large directory performance (700+ entries block the session goroutine; consider async/paginated enumeration)
- [ ] **Auto-spawn on USB connect (deferred).** macOS launchd supports
      `LaunchEvents → com.apple.iokit.matching` to spawn an agent when a
      matching USB device connects. Apple uses this for Image Capture etc.
      Wired through `SMAppService.agent`, it'd let users who quit
      Comprador have the app come back when they plug in a phone. But:
      "Start at Login" already covers this for the common case (most
      users enable it via the welcome window), the kernel-claim race
      against ptpcamerad is unaffected, and it adds a Login Items row.
      File this under "easy lever to pull *if* we see post-launch
      reports of 'I plugged in a phone and nothing happened.'" Until
      then, redundant.

## Known friction points

- [x] "Unsecured Connection" dialog on mount — fixed with `kNAUIOptionNoUI` + guest auth
- [x] Process names for the kill-before-claim were wrong: actual names on macOS Sequoia+
      are `ptpcamerad` / `AMPDeviceDiscoveryAgent` (not `PTPCamera`/`AMPDevicesAgent`).
      Kill is now done from inside the Go bridge with up to 6 retries to race
      launchd's ~60ms respawn window.
- [ ] PTPCamera must be killed before bridge can claim USB interface — works but inelegant
- [ ] `libusb_detach_kernel_driver` timeout adds ~5s on some connections
- [ ] **App-after-plug failure is unwinnable from any non-SIP-disabled path.**
      Diagnosed 2026-05-04 across multiple sessions; recording the dead ends
      so we don't re-walk them:

      Symptom: if Comprador starts *after* the phone is plugged in,
      `libusb_claim_interface` fails with `LIBUSB_ERROR_ACCESS` and stays
      failing across any number of retries, IOKit seizes, daemon kills,
      or USB resets. If Comprador is already running when the phone is
      plugged in, the bridge claims on attempt 1 — the bridge wins a
      race against `ptpcamerad`'s exclusive-access claim, but only in
      the first ~5–10 seconds after enumeration.

      Tried and failed:
      - `killall -9 ptpcamerad`: launchd respawns within ~60ms.
      - `IOUSBDeviceInterface500.USBDeviceOpenSeize`: returns
        `kIOReturnExclusiveAccess (0xE00002C5)`; IOKit refuses to evict
        an exclusive holder from userspace.
      - `libusb_detach_kernel_driver`: returns "Invalid argument";
        macOS doesn't support userspace driver detach.
      - `libusb_reset_device`: returns `LIBUSB_ERROR_NO_DEVICE`; the
        macOS reset path requires seized ownership.
      - `launchctl bootout gui/<UID>/com.apple.ptpcamerad`: refused
        with "Operation not permitted while System Integrity Protection
        is engaged". Even root cannot bootout Apple's
        `/System/Library/LaunchAgents` services with SIP on.

      Remaining options, all unacceptable for a consumer app:
      - Ship a kext (Apple deprecated kexts; user must disable SIP).
      - Ship a DriverKit extension (multi-week build, App Store review).
      - Tell the user to disable SIP.

      **Decision: accept the manual replug as the recovery path.** When
      the claim fails, the failure notification already tells the user
      to unplug and replug. The detach/attach cycle that follows fires
      a fresh USB enumeration, and the auto-retry path mounts cleanly.
      It's two seconds of physical action; not worth the consumer-hostile
      fixes above.
