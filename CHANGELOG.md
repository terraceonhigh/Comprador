# Changelog

## v0.1.1 — 2026-05-04

Distribution polish — same code as v0.1.0, signed and notarized.

- **Notarized by Apple.** First launch no longer requires the
  right-click → Open dance. Double-click installs cleanly past
  Gatekeeper.
- **Ships as a .dmg.** Drag-to-Applications window with the standard
  macOS install affordance, replacing the v0.1.0 .zip.
- Both the `.app` and the `.dmg` are stapled, so first-launch works
  fully offline.

## v0.1.0 — 2026-05-04

First public release.

### What works

- Mounts Android phones (any vendor with a known USB ID, or any phone
  that exposes a USB Still Image / PTP class interface) as a Finder
  volume.
- Mounts cameras (DSLRs and mirrorless from Canon, Nikon, Sony, Fuji,
  Olympus, Panasonic, etc.) the same way — anything libmtp recognises
  as MTP- or PTP-capable.
- Volume name comes from the device's friendly name (e.g. `Pixel 6`,
  set under Android *Settings → About → Device name*), not the IP.
- Optional privileged helper drops the `.local` suffix so volumes show
  up as `/Volumes/Pixel-6` instead of `/Volumes/Pixel-6.local`.
- Welcome window on first launch with phone and camera setup steps.
- "Show in Finder" menu item; auto-opens Finder on every successful
  mount; throbbing menu-bar icon while connecting.
- "Start at Login" toggle.

### Architecture

- Go WebDAV bridge (cgo against libmtp) serves the device's filesystem
  on a localhost port.
- Swift menu-bar app watches USB attach/detach via IOKit, spawns the
  bridge per-device, and mounts the WebDAV URL through NetFS.
- Volume is indistinguishable from any other Finder mount.

### Known issues

- **First-plug-after-app-start may need a manual replug.** When a phone
  is already connected at the moment Comprador starts, macOS's
  `ptpcamerad` may have already claimed the USB interface exclusively;
  the bridge can't displace it. The app shows a notification telling
  the user to unplug and replug, after which it auto-recovers. A
  DriverKit extension that beats `ptpcamerad` to the match is the
  permanent fix; on the roadmap.
- **Not notarized.** First launch requires right-click → Open to
  bypass Gatekeeper. Notarization needs an Apple Developer Program
  subscription, also on the roadmap.
- **Apple Silicon only.** No Intel build yet.
- **Single device at a time.**

### Build artifacts

- `Comprador.zip` — drag-to-Applications app bundle, ad-hoc signed,
  ~7 MB.
