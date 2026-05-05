# Changelog

## v0.2.3 — 2026-05-04

First notarized release. (v0.2.0–v0.2.2 were burned getting the
release pipeline through Apple's notary service — embedded binaries
needed individual signing with hardened runtime + secure timestamp
before the `.app` would pass.)

Distribution polish — same code as v0.1.0, signed and notarized.

- **Notarized by Apple.** First launch is just a double-click. No
  Gatekeeper warning, no right-click → Open dance.
- **Ships as a .dmg.** Standard drag-to-Applications window,
  replacing the v0.1.0 .zip.
- Both the `.app` and the `.dmg` are stapled, so first-launch works
  fully offline — no Apple-server roundtrip required.

(Skipped v0.1.1–v0.1.3 because those tags exist in the upstream
OpenMTP history this repo was forked from. Bumped to v0.2.x to
disambiguate.)

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
