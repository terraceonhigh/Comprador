# Comprador Architecture

## Overview

Comprador is a macOS menu bar application that makes an Android phone
appear as a mounted volume in Finder when connected via USB. It requires
no developer mode, no USB debugging, and no user action beyond selecting
"File Transfer" on the phone's USB notification.

## Components

```
┌──────────────────────────────────────────────────────────────────┐
│                          Comprador.app                            │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐             │
│  │ DeviceWatcher│─▶│BridgeProcess │─▶│MountManager │             │
│  │   (IOKit)    │  │  (Process)   │  │ (mount -t   │             │
│  └──────────────┘  └──────┬───────┘  │   nfs, loop)│             │
│         │                 │          └─────────────┘             │
│  ┌──────────────┐         │spawns                                 │
│  │ USBSeizer    │         ▼                                       │
│  │ (IOKit seize)│  ┌────────────────┐                             │
│  └──────────────┘  │  bridge binary │                             │
│                    │   (Go + cgo)   │                             │
│                    └───────┬────────┘                             │
└────────────────────────────┼──────────────────────────────────────┘
                             │
                             ▼
                  ┌──────────────────────────────────┐
                  │       libmtp / Galatea NFSv4      │
                  │                                   │
                  │   ┌──────────┐  ┌──────────────┐ │
                  │   │  libmtp  │  │ Galatea NFSv4│ │
                  │   │  (cgo)   │  │ server (mtp- │ │
                  │   └────┬─────┘  │  fsal FSAL)  │ │
                  │        │        └──────────────┘ │
                  └────────┼──────────────────────────┘
                           ▼
                      ┌──────────┐       ┌──────────┐
                      │  Phone   │       │  Finder  │
                      │  (USB)   │       │  mount   │
                      └──────────┘       └──────────┘
```

### Swift Menu Bar App (`MenuBarApp/`)

- **AppDelegate.swift** — Orchestrates the lifecycle: device attach →
  bridge start → mount. Device detach → unmount → bridge stop. Manages
  menu bar icon state. (The first-launch privileged-helper install prompt
  was removed in v0.4.0 along with the helper itself.)
- **DeviceWatcher.swift** — IOKit USB monitoring. Matches `IOUSBHostDevice`
  on attach if either (a) the vendor ID is in the known-Android-OEM list
  or (b) the device exposes an `IOUSBHostInterface` with `bInterfaceClass = 6`
  (USB Still Image / PTP class). Tracks emitted-attach `locationID`s so
  detach matches the same population. Fires callbacks on attach/detach.
- **BridgeProcess.swift** — Spawns the Go bridge binary from the app
  bundle's `Resources/` directory. Reads `PORT=N`, `HOST=name`, and
  `DEVICE=name` from stdout. Kills `ptpcamerad` / `AMPDeviceDiscoveryAgent`
  before claim attempts (the modern names — see MISTAKES.md). 20-second
  timeout with user notification if File Transfer mode isn't selected.
- **USBSeizer.swift** — IOKit-side companion to the bridge's process
  killing. Calls `IOUSBDeviceInterface::USBDeviceOpenSeize` to request
  exclusive access (terminating any other client of the device), then
  `USBDeviceReEnumerate(0)` to force a USB-level detach/reattach, and
  closes immediately so the bridge can `libusb_claim` the freed PTP
  interface on first attempt. Works without an admin password.
- **MountManager.swift** — Mounts the bridge's Galatea NFSv4 server via
  the unprivileged `mount -t nfs` path (loopback `127.0.0.1:port`,
  `vers=4.0`, mounted under a user-writable directory rather than
  `/Volumes`). Unmounts via `DiskArbitration` or fallback `umount`.
  `cleanupStaleMounts()` sweeps loopback-pointed NFS mounts (and legacy
  WebDAV/`/Volumes` paths) left by a previous crash so a restart doesn't
  accumulate auto-suffixed duplicates (`Pixel-6-1`, `-2`, …).
- **LoginItem.swift** — Wraps `SMAppService.mainApp` for "Start at
  Login" support.

### Go NFS Bridge (`bridge/`)

- **main.go** — Entry point. Binds a random localhost port, detects the
  MTP device, registers an mDNS hostname (`<DeviceName>.local`, the
  friendly name sanitised into a DNS-safe label by `mtp.RegisterHostname`)
  so the Finder volume label reads as the device name rather than
  `localhost`,
  and prints `PORT=N`, `HOST=name`, `PROTO=nfs`, `DEVICE=name` to stdout
  for the Swift parent to read. It then serves the phone over the
  statically-linked Galatea NFSv4 server (`galatea.ServeListener`) on
  the bound listener. Catches SIGINT/SIGTERM so cleanup defers actually
  run.
- **mtp/binding.go** — cgo bindings against libmtp. Device detection,
  file streaming (via C callbacks), folder creation, deletion. Uses the
  raw device detection API (`LIBMTP_Detect_Raw_Devices` +
  `LIBMTP_Open_Raw_Device_Uncached`) for better diagnostics. The open
  path retries up to 6 times with `killCompetingProcesses()` before each
  attempt, racing launchd's ~60ms respawn of `ptpcamerad`.
- **mtp/binding_callbacks.go** — C↔Go callback bridge for streaming file
  data. Matches the exact `MTPDataPutFunc`/`MTPDataGetFunc` signatures
  (5 parameters each).
- **mtp/session.go** — Single-goroutine MTP serialization. All MTP calls
  go through a request/response channel. Maintains a bidirectional object
  map (POSIX path ↔ MTP object handle) with lazy directory enumeration.
  `DeviceName()` returns `LIBMTP_Get_Friendlyname` falling back to
  `LIBMTP_Get_Modelname` then `LIBMTP_Get_Manufacturername`.
- **mtp/dnssd.go** — Spawns `dns-sd -P` to publish `<DeviceName>.local
  → 127.0.0.1` via mDNS. Used as the unprivileged fallback when the
  helper isn't installed; an orphan reaper kills any stale dns-sd
  subprocesses from a prior crashed run.
- **mtp/killers.go** — `killCompetingProcesses()`: SIGKILL `ptpcamerad`,
  `AMPDeviceDiscoveryAgent`, etc. macOS launchd respawns them in ~60ms,
  so this is called from within a tight retry loop right before
  `LIBMTP_Open_Raw_Device_Uncached`.
- **mtpfsal/mtpfsal.go** — Galatea's FSAL (the
  `github.com/terraceonhigh/galatea/pkg/virtual` Directory / Leaf / Node
  interfaces) implemented over a live MTP session, so the phone's object
  store is served as a userspace NFSv4 volume by `galatea.ServeListener`.
  `Root(session)` returns the FSAL root and a `HandleResolver`. Reads are
  ranged `VirtualRead`; writes are staged and committed to the device on
  an idle-flush timer (`bridge/staging`). Every `Virtual*` method that
  touches the device goes through `(*mtp.Session).Do` — libmtp is not
  thread-safe, so the single session goroutine is the serialization
  boundary. AppleDouble sidecars (`._*`, `.DS_Store`) are staged so
  Finder doesn't error, then dropped instead of written to the phone
  (`isAppleDouble`). This package replaced the deleted `bridge/webdav`
  and `bridge/nfs` (willscott NFSv3) substrates.
- **staging/staging.go** — In-memory write-staging registry: buffers a
  file being written over NFS until an idle-flush timer fires, then
  commits it to the device via `OpSendFile` (or drops it, for sidecars).
- **cmd/mdnstest/** — Tiny standalone binary that exercises the dns-sd
  wrapper without an MTP device attached. Useful for debugging the
  hostname-registration path in isolation.

### Privileged Helper — removed (v0.4.0)

There is no longer a privileged helper. Earlier versions shipped a root
`comprador-helper` LaunchDaemon (registered via `SMAppService.daemon`)
whose only job was to edit `/etc/hosts` so each device got a clean
`/Volumes/<name>` mount label. Once loopback NFS mounts were found to
work unprivileged, the helper was vestigial; it was removed in v0.4.0
to eliminate the bundle's largest privilege-escalation surface and the
admin-password prompt. The cost is cosmetic: volumes are now named from
the mDNS `<device>.local` source rather than a clean `/etc/hosts` label.

## Key Design Decisions

### Why NFS (and why NFSv4)?

macOS ships a native NFS client, so a loopback NFS server can be mounted
with an unprivileged `mount -t nfs` and surfaces in Finder's sidebar with
no kernel extensions, no File Provider complexity, and no entitlement pain
beyond USB access. NFSv4 specifically: its protocol floor tolerates the
multi-minute reads libmtp can produce on large files, which the earlier
patched `willscott/go-nfs` NFSv3 path did not — that path needed an entire
JUKEBOX/prefetch workaround to dodge NFSv3's RPC-timeout window, and both
were retired in v0.4.0. (The user-visible payoff: you can stream a video
straight off the phone — play it, scrub and seek in place — without copying
it to the Mac, which the NFSv3 path could not sustain.) The serving layer is
**Galatea**, an in-house
userspace NFSv4 server (a sibling project, statically linked into the
bridge); the original WebDAV server was also retired at the same time.

### Why a separate Go binary?

libmtp is a C library. cgo provides straightforward bindings. Go's
goroutines map cleanly onto the single-threaded MTP serialization
requirement. Galatea (the NFSv4 server) is pure Go and statically links
into the same binary. A single static binary is trivial to bundle.

### Why lazy enumeration?

A full recursive walk of a phone with thousands of files (YouTube Music
caches, photo thumbnails) takes minutes over MTP. Lazy enumeration fetches
directory contents only when Finder actually browses into them, making
startup near-instant.

### Why kill ptpcamerad / AMPDeviceDiscoveryAgent?

macOS launches `ptpcamerad` (and on newer macOS, `AMPDeviceDiscoveryAgent`)
when it detects a PTP/MTP USB device, claiming the USB interface before
libmtp can call `libusb_claim_interface`. Both are LaunchAgents that
launchd respawns in ~60ms after a SIGKILL, so the kill must happen *just
before* the claim — we run it from within the bridge in a tight retry
loop, not from Swift. The names matter: older Comprador code killed
`PTPCamera` and `AMPDevicesAgent`, which silently no-op on modern macOS.
On the Swift side `USBSeizer` complements this by seizing and
re-enumerating the device via IOKit so the kernel's own USBImaging
driver releases the PTP interface (see the `USBSeizer.swift` bullet).

### Why mDNS hostnames instead of a privileged `/etc/hosts` helper?

`mount -t nfs` (and NetFS before it) names a loopback mount from the
source host, and `/Volumes` itself is `root:wheel 0755`, so the
unprivileged app can't write an arbitrary mount label there. The bridge
registers a per-device `<name>.local` via mDNS — no privileges required —
so the Finder volume reads as the device name. Earlier versions ran a
privileged root helper to write a clean `/etc/hosts` entry and a
`/Volumes/<name>` mount; that helper was removed in v0.4.0 (see the
"Privileged Helper — removed" section above) because the mDNS path is
unprivileged and the only cost is a cosmetic `.local` suffix.

### Why source the hostname from libmtp's friendly name?

The mDNS hostname traces back to Android's
`Settings.Global.DEVICE_NAME` (the user-visible "Device name" under
Settings → About). That property is exposed over MTP as
`DeviceFriendlyName`, which `LIBMTP_Get_Friendlyname` returns. Going
through libmtp instead of the IOKit `USB Product Name` string means the
volume is named what the *user* called their phone, not what the OEM
embedded in the USB descriptor (some Samsung phones ship with internal
codes like `SM-S921B` rather than `Galaxy S24`).

## Data Flow

### File Download (NFSv4 READ)

```
Finder → NFSv4 READ → Galatea → mtpFile.VirtualRead(buf, offset)
  → session.Do(OpGetPartial, ObjectID, offset, len) → [session goroutine]
  → binding partial read → libmtp
  → goDataPutFunc callback → sliceWriter (fills buf)
  → READ reply
```

The read is ranged (`OpGetPartial`), serialised through the session
goroutine. NFSv4 tolerates the multi-minute reads a large file can take,
so there is no JUKEBOX/prefetch machinery (that was an NFSv3 workaround,
removed in v0.4.0).

### File Upload (NFSv4 OPEN/WRITE/commit)

```
Finder → NFSv4 OPEN(create) → Galatea → staging entry seeded
Finder → NFSv4 WRITE → mtpFile.VirtualWrite(buf, offset)
  → staging file WriteAt (no device I/O; resets idle timer)
idle-flush timer (≈2 s after last WRITE) → staging.Commit
  → session.Do(OpSendFile) → [session goroutine]
  → binding.SendFileFromReader() → LIBMTP_Send_File_From_Handler
  → goDataGetFunc callback → io.Reader
  → MTP device
```

The whole file is buffered in a staging temp file and sent on the idle
timer (MTP has no partial write). CLOSE is intentionally not the commit
trigger — the macOS NFSv4 client copies a file as OPEN(create) →
CLOSE(empty) → re-OPEN → WRITE → CLOSE, so committing on the first close
would send a 0-byte file.

### Directory Listing (NFSv4 READDIR)

```
Finder → NFSv4 READDIR → Galatea → mtpDir.VirtualReadDir()
  → session.EnsurePopulated(path)
    → if not cached: session.Do(OpListDir)
      → [session goroutine] → LIBMTP_Get_Files_And_Folders
      → populate ObjectMap, mark as populated
  → ObjectMap.ListChildren(path)
  → READDIR reply (entries reported via DirectoryEntryReporter)
```
