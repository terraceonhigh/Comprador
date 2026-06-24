# Comprador

**See Android phones and cameras in Finder. Free and open source.**

<p align="center">
  <img src="images/demo.png" alt="A Pixel 6 mounted as a Finder volume, showing the standard Android folder layout (Alarms, Android, Audiobooks, Books, DCIM, Documents, Download, Pictures, etc.)" width="640">
</p>

Plug in. Comprador mounts the device as a Finder volume — no extra app
to open, no kernel extension, no developer mode, no subscription.

Website: <https://terraceonhigh.github.io/Comprador/>

## How It Works

**Android phones**

1. Plug in your phone via USB
2. Pull down the notification shade → tap **File Transfer**
3. Phone appears in Finder sidebar

**Cameras (DSLRs and mirrorless — Canon, Nikon, Sony, Fuji, etc.)**

1. Plug in the camera via USB
2. If the camera asks, choose **PC / Computer** mode (sometimes called
   *MTP* or *PTP*)
3. Camera appears in Finder sidebar

That's it.

## Download

[**Download the latest release**](https://github.com/terraceonhigh/Comprador/releases/latest)
→ open the .dmg → drag `Comprador.app` to the Applications folder
shortcut → eject the disk image.

Comprador is signed and notarized by Apple, so the first launch is
just a double-click — no Gatekeeper warning, no right-click dance.

### Requirements

- macOS 13 (Ventura) or later
- Apple Silicon Mac (ARM)
- A data-capable USB cable
- An Android phone, or a camera that exposes itself as a USB
  storage / PTP device when plugged in

### Known issues

- **First-plug-after-app-start may fail.** If your phone is already
  connected when you launch Comprador, the bridge sometimes can't
  claim the USB interface (macOS's `ptpcamerad` has it). The app
  will tell you what to do: unplug and replug. After that, it works.
  The proper fix is a DriverKit extension, which is on the roadmap.
  Workaround for now: launch Comprador *before* plugging in.
- **Apple Silicon only** — no Intel build yet.

## Building from Source

```bash
# Prerequisites
brew install libmtp go

# With full Xcode installed:
make dist

# With only Command Line Tools (no Xcode):
make dist-swiftc

# Output: dist/Comprador.zip (~5 MB)
```

See [docs/BUILDING.md](docs/BUILDING.md) for full build instructions.

## Architecture

Comprador has two main components:

**Go Bridge** — A standalone binary that connects to the phone
via libmtp (cgo) and serves its filesystem from **Galatea**, an in-house
userspace NFSv4 server, on a random loopback port. macOS speaks NFSv4
natively, so no kernel extension is needed on the Mac side.

**Swift Menu Bar App** — Watches for USB devices via IOKit (matched by
known Android vendor IDs *or* USB Still Image / PTP class), seizes the
PTP interface, spawns the bridge when an MTP/PTP device is detected, and
mounts the bridge's NFS endpoint as a Finder volume via
`/sbin/mount -t nfs` to localhost (no privileged helper required —
verified on macOS 13+).

```
Phone ←USB→ libmtp ←cgo→ Go bridge (Galatea NFSv4) ←localhost→ Finder
                                ↑
                         hostname from
                       Settings.Global.DEVICE_NAME
                       (via libmtp friendly name)
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

## What Works

- Browse phone filesystem in Finder
- Copy files from phone to Mac (drag & drop or cp)
- Copy files from Mac to phone
- Create folders on phone
- Delete files and folders on phone
- Menu bar icon with device status
- Automatic mount on plug-in, unmount on unplug
- Volume named after the device (the same name you set under Android
  Settings → About → Device name, via MTP `DeviceFriendlyName`)
- "Start at Login" toggle via `SMAppService.mainApp`
- Self-heals if the bridge dies: unmounts the stale volume, respawns,
  and remounts
- Picks up most cameras (PTP class), not just Android phones — libmtp
  decides what's actually mountable
- Concurrent multi-device: plug in two phones (or a phone and a camera)
  and browse both at once, each its own volume in the sidebar
- Stream media in place: play or scrub a video straight off the phone
  without copying it to the Mac first

See [TODO.md](TODO.md) for the full roadmap.

## Automation

There is no app-specific scripting API, and none is needed: Comprador mounts the
phone as a real volume, so any tool that works on a folder works on it. `rsync`,
`cp`, `find`, `ditto`, cron jobs, and Hazel rules all operate on the mount point
directly.

Eject from a script with `diskutil unmount` (or `umount`) on the mount path;
Comprador notices the external unmount and shuts its bridge down cleanly. Find
the path with `mount | grep Comprador`.

## FAQ

**Where do my files actually live?**
On your phone. Comprador only mounts a *view* — nothing is copied to
your Mac unless you drag a file into a local Finder window. Closing the
volume doesn't lose anything; opening it again shows the current state
of the phone.

**Does Comprador see my files? Are they uploaded anywhere?**
No. Everything is local: your phone over USB, Comprador as a small
NFS server bound to `localhost`, Finder as the NFS client. No cloud,
no telemetry, no internet round-trips. The bridge binds only to the
loopback interface and can't be reached from the LAN, let alone the
public internet.

**Does it work over Wi-Fi?**
No. USB only. Wireless MTP is technically possible but isn't widely
supported on the Android side, and adding the protocol surface would
roughly double the attack surface for very little gain.

**Does it work with iPhones?**
No. iPhones don't speak MTP — they speak Apple's proprietary mobile
device protocol, handled by Image Capture / Photos / iCloud / Finder
sync. Comprador is for *non*-Apple phones and PTP/MTP cameras.

**How do I uninstall?**
Drag `Comprador.app` from `/Applications` to the Trash. The Login Item
registration goes with it. There is no privileged helper, daemon, or
system modification to clean up — Comprador never installs one.

**Finder shows the volume but no files (or transfers stall).**
The MTP session has gotten into a bad state — usually because the
phone slept or the cable wiggled. Click the menu bar icon → **Eject**,
then unplug and replug. Comprador re-mounts fresh.

**How do I update?**
Download the latest `.dmg` from
[Releases](https://github.com/terraceonhigh/Comprador/releases/latest)
and replace the app in Applications. Sparkle-style auto-update is on
the roadmap.

## Why Not...

**ADB?** Requires enabling Developer Options (Settings → About → tap Build
Number 7 times). Too much friction for non-technical users.

**macFUSE?** Requires a kernel extension, which means disabling SIP or
navigating Gatekeeper. Not acceptable for a consumer app.

**Android File Transfer?** Google quietly orphaned it — removed the
download link in early 2024, and that page now serves a Windows-only
app. It's unmaintained and breaks on Apple Silicon / recent macOS.

**File Provider API?** Designed for cloud storage. MTP's stateful,
session-locked protocol doesn't map well to File Provider's pull-based model.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) — Component design, data flow
- [Building](docs/BUILDING.md) — Prerequisites, build targets
- [Testing](docs/TESTING.md) — Test suites, manual testing, debugging
- [Mistakes](docs/MISTAKES.md) — 41 pitfalls we hit and how we fixed them

## Credits

Comprador began as a fork of [OpenMTP](https://github.com/ganeshrvel/openmtp)
by Ganesh Rathinavel, but no source from that project remains — Comprador
is a clean reimplementation with a different architecture (a Go bridge
serving NFSv4 + a Swift menu bar app, in place of Electron + Node.js).

## Support

Comprador is free and will stay that way. If it's saved you the cost of
a third-party transfer app, or just made your day a little less annoying,
you can throw a few dollars in the tip jar:

- **Interac e-Transfer** (Canadian banking only) → `terrace@terrace.zone`.
  Auto-deposit is on, so no security question is needed; the transfer
  lands directly. Any amount is welcome and goes toward the project's
  ongoing costs (Apple Developer Program annual renewal, signing
  certificates, the domain).

If you'd rather contribute code or testing, see
[CONTRIBUTING](CONTRIBUTING.md).

## License

[GNU General Public License, version 3 or later](LICENSE).

Third-party components and their licenses (libmtp, Galatea) are listed
in [NOTICES](NOTICES.md).
