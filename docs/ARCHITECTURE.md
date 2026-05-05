# AndroidFS Architecture

## Overview

AndroidFS is a macOS menu bar application that makes an Android phone
appear as a mounted volume in Finder when connected via USB. It requires
no developer mode, no USB debugging, and no user action beyond selecting
"File Transfer" on the phone's USB notification.

## Components

```
┌──────────────────────────────────────────────────────────────────┐
│                          AndroidFS.app                            │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐             │
│  │ DeviceWatcher│─▶│BridgeProcess │─▶│MountManager │             │
│  │   (IOKit)    │  │  (Process)   │  │  (NetFS)    │             │
│  └──────────────┘  └──────┬───────┘  └─────────────┘             │
│                           │                                       │
│  ┌──────────────┐         │spawns                                 │
│  │HelperClient  │         ▼                                       │
│  │ (Unix sock)  │  ┌────────────────┐                             │
│  └──────┬───────┘  │  bridge binary │                             │
│         │          │   (Go + cgo)   │                             │
│         │          └───────┬────────┘                             │
└─────────┼──────────────────┼──────────────────────────────────────┘
          │                  │
          ▼                  ▼
┌──────────────────┐  ┌──────────────────────────────────┐
│ androidfs-helper │  │           libmtp / WebDAV         │
│  (LaunchDaemon)  │  │                                   │
│                  │  │   ┌──────────┐  ┌──────────┐     │
│  edits /etc/hosts│  │   │  libmtp  │  │  WebDAV  │     │
│  managed block   │  │   │  (cgo)   │  │  server  │     │
└──────────────────┘  │   └────┬─────┘  └──────────┘     │
                      │        │                          │
                      └────────┼──────────────────────────┘
                               ▼
                          ┌──────────┐       ┌──────────┐
                          │  Phone   │       │  Finder  │
                          │  (USB)   │       │  mount   │
                          └──────────┘       └──────────┘
```

### Swift Menu Bar App (`MenuBarApp/`)

- **AppDelegate.swift** — Orchestrates the lifecycle: device attach →
  bridge start → optional `helper.addHost(<DeviceName>)` → mount.
  Device detach → unmount → `helper.removeHost` → bridge stop. Manages
  menu bar icon state and the first-launch helper-install prompt.
  Sanitises libmtp's `Friendlyname` into a DNS label.
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
- **MountManager.swift** — Mounts the WebDAV server via `NetFSMountURLSync`
  with guest auth and no-UI options. Unmounts via `DiskArbitration` or
  fallback `umount`. Sweeps stale loopback-pointed webdav mounts at
  app startup so a previous crash doesn't leave us with auto-suffixed
  duplicates (`/Volumes/Pixel-6-1`, `-2`, …).
- **HelperClient.swift** — Talks to the privileged helper over its Unix
  socket. Wraps `SMAppService.daemon(plistName:)` for registration and
  exposes `addHost(name)` / `removeHost(name)` / `clearHosts()`.
- **LoginItem.swift** — Wraps `SMAppService.mainApp` for "Start at
  Login" support.

### Go WebDAV Bridge (`bridge/`)

- **main.go** — Entry point. Binds a random localhost port, detects the
  MTP device, starts the WebDAV server, registers an mDNS hostname
  (`<DeviceName>.local`) as the safety-net naming path, and prints
  `PORT=N`, `HOST=name`, `DEVICE=name` to stdout for the Swift parent
  to read. Catches SIGINT/SIGTERM so cleanup defers actually run.
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
- **webdav/handler.go** — Implements `golang.org/x/net/webdav.FileSystem`.
  Translates WebDAV operations to MTP operations. Handles file upload via
  buffered `mtpNewFile` (buffers entire file, sends on Close).
- **webdav/finder.go** — Intercepts Finder probe files (`.DS_Store`,
  `._*`, `.Spotlight-V100`, etc.) and returns 404 without touching MTP.
- **cmd/mdnstest/** — Tiny standalone binary that exercises the dns-sd
  wrapper without an MTP device attached. Useful for debugging the
  hostname-registration path in isolation.

### Privileged Helper (`helper/`)

A small Go binary registered with launchd via `SMAppService.daemon`. The
host app's bundle ships:
- `Contents/MacOS/androidfs-helper` — the helper executable
- `Contents/Library/LaunchDaemons/com.androidfs.helper.plist` — the
  LaunchDaemon definition

After the user approves the daemon in System Settings → Login Items, it
runs as root and listens on a Unix socket at
`/var/run/androidfs-helper.sock` (mode 0666). Protocol is line-based:

    ADD <name>      → 127.0.0.1 <name> appended to managed block
    REMOVE <name>   → matching line removed
    CLEAR           → entire managed block removed
    PING            → liveness check
    Replies: "OK" or "ERR <reason>"

`<name>` must match `^[A-Za-z][A-Za-z0-9-]{0,62}$` and not collide with
reserved labels (`localhost`, `broadcasthost`, etc.) — single-label,
non-dotted names that can't impersonate real domains.

Hosts file edits are atomic via `tempfile + rename` and fenced by an
`# AndroidFS BEGIN ... # AndroidFS END` block so the helper never
touches lines outside its scope.

## Key Design Decisions

### Why WebDAV?

macOS Finder mounts WebDAV natively and surfaces it in the sidebar. No
kernel extensions, no File Provider complexity, no entitlement pain beyond
USB access. A `localhost` WebDAV server mounted via `NetFSMountURLSync` is
indistinguishable to the user from any other volume.

### Why a separate Go binary?

libmtp is a C library. cgo provides straightforward bindings. Go's
goroutines map cleanly onto the single-threaded MTP serialization
requirement. The `golang.org/x/net/webdav` package handles most WebDAV
protocol details. A single static binary is trivial to bundle.

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
loop, not from Swift. The names matter: older AndroidFS code killed
`PTPCamera` and `AMPDevicesAgent`, which silently no-op on modern macOS.

### Why a privileged helper for `/etc/hosts`?

NetFS auto-names a WebDAV mount from the URL host (`http://x:port/` →
`/Volumes/x`), with no public API to override. `/Volumes` itself is
`root:wheel 0755`, so the unprivileged app can't write a custom mount
point. mDNS lets the bridge resolve `<name>.local` without privileges
but the suffix shows up in Finder.

The helper, registered via `SMAppService.daemon`, lets the user grant
one-time approval ("AndroidFS Helper" in Login Items). After that, every
device gets a clean single-label name in /etc/hosts and a clean
`/Volumes/<name>` mount, with no further prompts. The narrow ADD/REMOVE
protocol and strict server-side validation mean even a malicious client
on the user's machine can't impersonate real domains via the helper.

### Why source the hostname from libmtp's friendly name?

The hostname registered with the helper traces back to Android's
`Settings.Global.DEVICE_NAME` (the user-visible "Device name" under
Settings → About). That property is exposed over MTP as
`DeviceFriendlyName`, which `LIBMTP_Get_Friendlyname` returns. Going
through libmtp instead of the IOKit `USB Product Name` string means the
volume is named what the *user* called their phone, not what the OEM
embedded in the USB descriptor (some Samsung phones ship with internal
codes like `SM-S921B` rather than `Galaxy S24`).

## Data Flow

### File Download (GET)

```
Finder → HTTP GET → webdav.Handler → mtpFile.Read()
  → session.Do(OpGetFile) → [session goroutine]
  → binding.GetFileToWriter() → LIBMTP_Get_File_To_Handler
  → goDataPutFunc callback → io.Writer (bytes.Reader)
  → HTTP response body
```

### File Upload (PUT)

```
Finder → HTTP PUT → webdav.Handler → mtpNewFile.Write()
  → bytes.Buffer (accumulate entire file)
  → mtpNewFile.Close()
  → session.Do(OpSendFile) → [session goroutine]
  → binding.SendFileFromReader() → LIBMTP_Send_File_From_Handler
  → goDataGetFunc callback → io.Reader (bytes.Reader)
  → MTP device
```

### Directory Listing (PROPFIND)

```
Finder → HTTP PROPFIND Depth:1 → webdav.Handler → mtpDir.Readdir()
  → session.EnsurePopulated(path)
    → if not cached: session.Do(OpListDir)
      → [session goroutine] → LIBMTP_Get_Files_And_Folders
      → populate ObjectMap, mark as populated
  → ObjectMap.ListChildren(path)
  → XML multistatus response
```
