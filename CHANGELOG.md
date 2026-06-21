# Changelog

## v0.4.0 — 2026-06-08

The substrate swap. Comprador now serves the phone over **Galatea**, an
in-house userspace NFSv4 server, replacing both earlier Finder layers — the
original WebDAV server and the patched `willscott/go-nfs` NFSv3 server (and,
with NFSv3, the entire JUKEBOX/prefetch workaround for its RPC-timeout window).
NFSv4's floor tolerates multi-minute libmtp reads, so that machinery is gone.

**Now working, end to end, live on a Pixel 6:**

- **Read** — browse + stream, no JUKEBOX (a 95 MB file clean; a 1 GB file read).
- **Write** — drag a file to the phone in Finder, byte-identical (a 1.07 GB
  Shrek.mp4 committed in a single transfer).
- **Full file management** — New Folder, delete, replace/overwrite, rename files
  and folders (instant, in-place via `LIBMTP_Set_Object_Filename`), move files
  between folders, and recursive folder move.

**Resilience:**

- The app **self-heals** when the bridge dies: it detects the exit, unmounts the
  stale volume, re-spawns and remounts (bounded retry), so a crash blips instead
  of hanging Finder.
- **Orphaned-bridge reaper** clears subprocesses left by a prior run that were
  contending for the USB interface — verified to let a relaunch seize without a
  physical replug.

**Correctness fixes:**

- Files no longer revert to 0 bytes when opened (a staging entry was shadowing
  the real size); the data-loss path it implied (an empty commit overwriting a
  real file) is guarded.
- The NFSv4 mandatory-attribute panic that could crash the bridge is closed via
  a single attribute chokepoint.
- Finder reports accurate free space (statfs), so drag-and-drop pre-flight works.
- Changes made on the phone while a folder is open in Finder — a photo just taken,
  a file deleted in the phone's Files app — now surface within a few seconds
  instead of requiring a physical replug.

**Removed:** the WebDAV server (`bridge/webdav`), the willscott NFSv3 path
(`bridge/nfs`) and its prefetch cache, their vendored dependencies, and **the
privileged root helper** (`comprador-helper` + its `SMAppService` daemon). The
helper existed to launder root for `mount_nfs`; once we found loopback NFS mounts
work unprivileged it was vestigial, kept only for a cosmetic `/etc/hosts` volume
label. Removing it eliminates the bundle's largest privilege-escalation surface
and the admin-password prompt; the cost is that volumes are named from mDNS
(`<device>.local`) rather than a clean `/etc/hosts` label.

**Known limitations:** verified on two vendors (Pixel 6 and Sony Xperia), not yet
an exhaustive multi-vendor sweep; and a USB-interface lock can still require a
physical replug specifically across system sleep/wake. See
[docs/PRE-LAUNCH.md](docs/PRE-LAUNCH.md).

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
