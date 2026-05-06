# Comprador — TODO

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

## Medium impact (reliability)

- [ ] Error recovery — detect bridge crash mid-session, auto-restart
- [ ] Handle detach during file transfer gracefully (don't hang Finder)
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
