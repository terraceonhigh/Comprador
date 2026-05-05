# Comprador

**See Android phones and cameras in Finder. Free and open source.**

Plug in. Comprador mounts the device as a Finder volume — no extra app
to open, no kernel extension, no subscription.

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
→ unzip → drag `Comprador.app` to your Applications folder.

**First launch is the awkward bit** because the app isn't yet
notarized by Apple:

1. Open Finder, navigate to Applications.
2. **Right-click** (or Control-click) `Comprador.app` → **Open**.
3. macOS shows a warning that the developer can't be verified.
   Click **Open** anyway. (You only do this once.)
4. Comprador appears in your menu bar with a small drive icon.

After that, plug in your phone or camera and follow the steps above.

> **Why the warning?** Notarized macOS distribution requires an Apple
> Developer Program subscription, which is on the roadmap. Until then
> you're trusting the binary you downloaded matches the open-source
> code in this repo. If you'd rather not trust it, build from source
> — instructions below.

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
- **Single device at a time.**

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

Comprador has three components:

**Go WebDAV Bridge** — A standalone binary that connects to the phone
via libmtp (cgo) and serves its filesystem over HTTP WebDAV on localhost.

**Swift Menu Bar App** — Watches for USB devices via IOKit (matched by
known Android vendor IDs *or* USB Still Image / PTP class), spawns the
bridge when an MTP/PTP device is detected, and mounts the WebDAV server
as a Finder volume via `NetFSMountURLSync`.

**Privileged Helper** *(optional)* — A small Go LaunchDaemon, registered
via `SMAppService.daemon`, that owns a managed block in `/etc/hosts`. It
lets the bridge advertise URLs like `http://Pixel-6:port/` so Finder
mounts the volume as `/Volumes/Pixel-6` instead of `/Volumes/Pixel-6.local`.
The user is prompted once on first launch; without the helper the app
falls back to mDNS-registered `<DeviceName>.local` hostnames.

```
Phone ←USB→ libmtp ←cgo→ Go bridge ←HTTP→ WebDAV ←mount→ Finder
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
- Optional privileged helper that drops the `.local` suffix from the
  Finder sidebar (`/Volumes/Pixel-6` instead of `/Volumes/Pixel-6.local`)
- Picks up most cameras (PTP class), not just Android phones — libmtp
  decides what's actually mountable

## Known Limitations

- First connection takes ~15-30 seconds (USB interface settling)
- Large directories (700+ files) are slow to enumerate (MTP protocol limitation)
- ARM Macs only (no Intel build yet)
- Not notarized (requires right-click → Open on first launch)
- Without the helper, volume name keeps a `.local` suffix
  (`Pixel-6.local` instead of `Pixel-6`)

See [TODO.md](TODO.md) for the full roadmap.

## Why Not...

**ADB?** Requires enabling Developer Options (Settings → About → tap Build
Number 7 times). Too much friction for non-technical users.

**macFUSE?** Requires a kernel extension, which means disabling SIP or
navigating Gatekeeper. Not acceptable for a consumer app.

**Android File Transfer?** Abandoned by Google, buggy, 4GB file limit.

**File Provider API?** Designed for cloud storage. MTP's stateful,
session-locked protocol doesn't map well to File Provider's pull-based model.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) — Component design, data flow
- [Building](docs/BUILDING.md) — Prerequisites, build targets
- [Testing](docs/TESTING.md) — Test suites, manual testing, debugging
- [Mistakes](docs/MISTAKES.md) — 23 pitfalls we hit and how we fixed them

## Credits

Comprador began as a fork of [OpenMTP](https://github.com/ganeshrvel/openmtp)
by Ganesh Rathinavel, but no source from that project remains — Comprador
is a clean reimplementation with a different architecture (Go WebDAV
bridge + Swift menu bar app, in place of Electron + Node.js).

## Support

Comprador is free and will stay that way. If it's saved you the cost of
a third-party transfer app, or just made your day a little less annoying,
you can throw a few dollars in the tip jar:

- [GitHub Sponsors](https://github.com/sponsors/terraceonhigh) — recurring
  or one-time, no fees taken out of your contribution.

Even small recurring sponsorships meaningfully cover the project's
infrastructure costs (Apple Developer Program enrollment, signing
certificates, the domain you're reading this on if there is one).
If you'd rather contribute code or testing, see
[CONTRIBUTING](CONTRIBUTING.md).

## License

[GNU General Public License, version 3 or later](LICENSE).

Third-party components and their licenses (libmtp, `golang.org/x/net`)
are listed in [NOTICES](NOTICES.md).
