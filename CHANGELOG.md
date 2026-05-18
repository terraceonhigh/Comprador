# Changelog

## v0.3.3 — 2026-05-18

The capability release on top of the v0.3.0 NFS pivot. Two
pre-launch blockers cleared in this stretch: the first-drag-after-
mount NFS READ stall, and concurrent multi-device support. Plus
the polish layer that the post-pivot quiet brought into focus
(per-storage quota, phone-side change reflection, AppleDouble
filtering).

### What's new

- **First-drag-after-mount no longer stalls.** Previously the
  bridge silently dropped every NFSv3 READ that resolved to a
  multi-GB phone-resident file, leaving Finder spinning on
  "Preparing to copy" until the macOS NFS client gave up. Three
  composing fixes: a `.metadata_never_index` sentinel that blocks
  Spotlight's pre-indexing pass, an `NFS3ERR_JUKEBOX` ("media not
  ready, retry later") response on READ for files above 50 MB,
  and an async prefetch that downloads in the background so the
  client's retries land on a ready file. End-to-end verified: VLC
  opens a 9 GB phone-resident video in ~6 min instead of hanging
  forever; small-file drags still land in 2–3 s.
- **Concurrent multi-device support.** Plug in two phones, get
  two Finder sidebar entries, browse and transfer in parallel.
  Cross-device drag-drop (Xperia SD card → Pixel Internal, and
  vice versa) works through the mount. The single-device guard
  in `AppDelegate.handleDeviceAttached` is relaxed; each device
  gets its own `DeviceSession`, its own bridge process, its own
  mDNS hostname, its own mount path.
- **Per-storage quota.** Finder's "X GB available" string now
  reports the actual free space of the storage you're standing in
  (Internal vs SD card), not an aggregate across both. Eliminates
  the cardinal sin where Finder green-lit a copy onto a near-full
  SD card because "105 GB free" summed Internal + SD.
- **Phone-side changes surface in Finder.** Delete a file via the
  phone's own Files app and the next directory listing through
  Comprador's mount drops it within ~2 seconds. Previously the
  bridge cached the phone's filesystem from session start and
  never reconciled.
- **AppleDouble `._*` files no longer reach the phone.** Finder
  writes companion `._<name>` files alongside copies; the phone
  has no use for them. Comprador now filters them server-side.
- **Clickable build identifier.** The Build menu item copies
  `BuildInfo.id` to the clipboard on click — useful when filing
  bug reports.

### Known and disclosed

- **First-plug-after-app-start may still fail.** If a phone is
  already plugged in when Comprador launches, the bridge may lose
  the USB-claim race against macOS's `ptpcamerad`. The welcome
  window discloses the recovery (unplug, replug); the bridge now
  fails fast on first failure rather than degrading the phone's
  USB state with retry cycles.
- **`.local` suffix in Finder sidebar.** Per-device mount sources
  are `<DeviceName>.local` (e.g. `Pixel-6.local`) carried from
  the v0.3.0 pivot. Slated for resolution in v0.4.0 with the
  helper retirement.

### Under the hood

- **Async prefetch on JUKEBOX.** When the bridge returns
  `NFS3ERR_JUKEBOX` it kicks off a background download of the
  full object via libmtp. Subsequent retries from the NFS client
  land on a ready file. Unhangs direct-read clients (VLC, ffprobe)
  that ignore JUKEBOX-as-retry-hint and treat the empty response
  as a hard read failure.
- **IOKit Location ID reconstruction.** The bridge accepts a
  `--device-loc-id` flag and reconstructs the macOS-format
  Location ID from libusb's bus_number + port chain, so multiple
  bridges can disambiguate which device on the bus they own.
- **Vendored go-nfs patched** to thread the requesting path into
  `Handler.FSStat` for per-storage quota; new hooks
  (`ReadSyncThreshold`, `ReadJukeboxSizeFn`, `ReadJukeboxBeginFn`)
  glue the JUKEBOX + prefetch behavior into the READ path. Worth
  upstreaming once field-test confidence is built.
- **`cleanupStaleMounts` regex widened** to recognize per-device
  `.local` NFS sources, not just `127.0.0.1:/` / `localhost:/`.
  Fixes stale Finder sidebar entries persisting across app
  restarts.

### Developer-only

- **`make test-md5`** — phone-side md5 verification harness via
  `adb shell md5sum`, gated by `COMPRADOR_TESTING_ADB=1`.
  Bypasses the bridge so a bridge-side bug can't mask itself by
  being self-consistent.
- **`make bridge-test`** — Go unit tests in `bridge/mtp/`
  covering ObjectMap reconciliation (TTL transitions, recursive
  removal, prefix-collision safety).
- **`SWIFT_DEBUG=1`** gate for `-D DEBUG` in `app-swiftc`.
  Production builds via `dist-swiftc` still inherit it
  inadvertently — see V0.4.0-DRAFT for the cleanup.

### Research notes

- **ImageCaptureCore investigation closed** without architectural
  change. Tests 1 (coexistence with ptpcamerad) and 2 (read
  throughput) passed empirically, but PTP-mode phones expose only
  camera content — the filesystem regions Comprador exists to
  address (Music, Downloads, app data) are unreachable. Receipt
  in `docs/RESEARCH-IMAGECAPTURECORE.md`; decision in
  `docs/DECISIONS.md`.
- **FUSE-T deferred indefinitely.** `docs/INVISIBILITY.md`
  inventories the thumb-drive gap; the application-layer fixes in
  this release substantially close it. The closed-source binary +
  ambiguous license + audit/fork/maintenance risk argued against
  the substrate change.
- **In-house FUSE-T equivalent research.** Spawned as a separate
  project (`~/Labs/Galatea`) with Daedalus as the agent. Buildbarn
  ships an Apache-2.0 NFSv4.0+4.1 server in Go with macOS mount
  recipe; 3–6 months part-time for one solo engineer. Not on
  Comprador's roadmap; runs in parallel.

### Narrative

Three letters cover the arc, in `correspondence/`:

- `12-autonomous-afternoon-2026-05-11` — multi-storage quota +
  phone-side reflection
- `13-end-of-day-2026-05-11` — ImageCaptureCore closure
- `14-three-wrong-framings` — the NFS READ stall debugging arc

## v0.3.2 — 2026-05-09

The working tag for v0.3.1's entitlements hotfix.

v0.3.1 was pushed against a stale local `origin/master` ref — the
PR #9 merge had landed on GitHub but the local fetch hadn't picked
it up, so `git tag -a v0.3.1 origin/master` resolved to the v0.3.0
commit. CI rebuilt the same broken code under the new tag. v0.3.2
re-tags the same fix at the correct commit (after `git fetch
origin`) so the released `.dmg` actually contains it.

No code changes from the v0.3.1 PR. Same NFS pivot as v0.3.0.

Process notes captured in [docs/BUILDING.md § "Process lessons from
v0.3.1's wrong-commit tag"](docs/BUILDING.md). The CI smoke-test
proposed there is filed as item #7 in the [v0.3.3 polish
plan](docs/V0.3.3.md).

## v0.3.1 — 2026-05-09

Hotfix: v0.3.0's released `.dmg` was rejected at launch by AMFI with
`-413 "No matching profile found"`. The CI workflow signed the bundle
with the production entitlements file (`Comprador.entitlements`),
which contains `com.apple.developer.system-extension.install` for the
planned DriverKit USB extension. That entitlement requires an
embedded provisioning profile, which the workflow doesn't provision.

Switch CI to sign with `Comprador.debug.entitlements` (no
system-extension key, no profile required). The DriverKit feature
isn't shipping in v0.3.x; restore the production entitlements when
the extension is ready and the provisioning profile is wired into the
secrets store.

No changes to runtime behavior. Same NFS pivot as v0.3.0.

## v0.3.0 — 2026-05-09

NFSv3 replaces WebDAV as the default mount surface.

- **No more 90-second mount wait.** Mount time is sub-second on macOS
  15.4+. The WebDAV path's quota PROPFIND chokepoint is gone.
- **Helper-free architecture.** `mount -t nfs` to localhost works for
  unprivileged callers on macOS — verified empirically. The privileged
  SMAppService daemon that v0.2.x shipped is no longer involved in the
  mount path. First-launch friction is reduced; no Login Items
  approval prompt is needed for normal use.
- **Per-device sidebar label.** The mount source is `<DeviceName>.local`
  (e.g. `XQ-BT52.local`) so Finder's Locations sidebar shows a
  meaningful per-device entry. The `.local` suffix is a known cosmetic
  carried into the v0.3.1 polish list.
- **Live re-enumeration.** Drag a file in Finder, see it appear in the
  directory listing within a couple seconds — no manual refresh. The
  bridge bumps the parent directory's mtime on every commit/delete/
  rename/mkdir so NFS clients invalidate their cached READDIR.
- **Recursive folder copies.** Deep tree drags (verified against a
  real git repo with `.git/objects/...`) re-enumerate each
  subdirectory as files commit.

### Under the hood

- Vendored `willscott/go-nfs` with one patch (`nfs_onwrite.go` responds
  `unstable` instead of `fileSync`) so macOS NFSv3 clients know they
  must follow up writes with a COMMIT RPC.
- 2-second idle-flush timer in the bridge's staging registry, since
  macOS clients are unreliable about sending COMMIT spontaneously.
- `fs.Rename` implemented with fast-path staging-rekey and slow-path
  MTP copy+delete.
- The privileged helper (`comprador-helper`) is still bundled for
  legacy WebDAV cosmetics but is no longer invoked on the NFS path.
  Slated for full removal in v0.4.0 once the WebDAV path is retired.

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
