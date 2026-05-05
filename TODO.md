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
