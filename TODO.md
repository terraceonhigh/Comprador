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
- [ ] Notarization build configuration (hardened runtime + signing for distribution)
- [ ] Large directory performance (700+ entries block the session goroutine; consider async/paginated enumeration)

## Known friction points

- [x] "Unsecured Connection" dialog on mount — fixed with `kNAUIOptionNoUI` + guest auth
- [x] Process names for the kill-before-claim were wrong: actual names on macOS Sequoia+
      are `ptpcamerad` / `AMPDeviceDiscoveryAgent` (not `PTPCamera`/`AMPDevicesAgent`).
      Kill is now done from inside the Go bridge with up to 6 retries to race
      launchd's ~60ms respawn window.
- [ ] PTPCamera must be killed before bridge can claim USB interface — works but inelegant
- [ ] `libusb_detach_kernel_driver` timeout adds ~5s on some connections
- [ ] **First-plug failure is unwinnable from libusb.** Captured 2026-05-04
      with descriptor logging (`bridge/mtp/usbinfo.go`): the kernel binds
      its USB Imaging Class driver to a class-6 PTP interface within
      microseconds of enumeration. By the time we spawn the bridge,
      ptpcamerad is *not* the holder — the kernel driver is — and macOS
      forbids userspace from detaching kernel drivers (`libusb_detach_kernel_driver`
      returns "Invalid argument", `libusb_reset_device` returns
      `LIBUSB_ERROR_NO_DEVICE` because the call requires seized ownership
      we don't have).

      Physical unplug+replug works because the kernel re-binds with a
      brief unclaimed window the bridge wins on attempt 1. Software
      cannot reproduce this without IOKit.

      **Proper fix:** Swift-side preflight using `IOUSBInterfaceOpenSeize`
      from IOKit (probably in `BridgeProcess.start()` before exec). Seize
      forces the kernel to release its claim so libusb can claim cleanly
      from the bridge process. Estimated 1–2 days: write the IOKit dance
      in Swift, hand off the seized state to the bridge process (probably
      by reopening from libusb's side immediately after Swift releases),
      validate against Pixel + Samsung + a camera. Until this lands,
      first-plug failure stays a manual-replug problem.
