<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Comprador USB Driver Extension — Design

**Author:** Claude Dexter (DriverKit specialist persona)
**Status:** scaffolding only; entitlement gate not yet cleared
**Companion docs:** [HANDOFF-DRIVERKIT.md](HANDOFF-DRIVERKIT.md),
[ENTITLEMENT-REQUEST.md](ENTITLEMENT-REQUEST.md)

This document fixes the architecture for the dext that solves the
first-plug-after-app-start failure mode (TODO.md → "App-after-plug
failure is unwinnable from any non-SIP-disabled path"). Read
[HANDOFF-DRIVERKIT.md](HANDOFF-DRIVERKIT.md) first for the *why*.
This document is the *what* and *how*.

---

## Bundle layout

```
Comprador.app/
├── Contents/
│   ├── Info.plist
│   ├── MacOS/
│   │   └── Comprador
│   ├── Resources/
│   │   └── bridge                ← Go bridge (Mercer territory)
│   ├── Frameworks/
│   │   ├── libmtp.9.dylib
│   │   └── libusb-1.0.0.dylib
│   ├── Library/
│   │   ├── LaunchDaemons/
│   │   │   └── com.comprador.helper.plist  ← /etc/hosts helper
│   │   └── SystemExtensions/                  ← NEW — dext lives here
│   │       └── com.comprador.app.USBDriver.dext/
│   │           ├── Contents/
│   │           │   ├── Info.plist
│   │           │   ├── MacOS/
│   │           │   │   └── com.comprador.app.USBDriver
│   │           │   └── _CodeSignature/
│   └── _CodeSignature/
```

The dext is a `.dext` bundle (a system-extension bundle with an
embedded executable) placed at `Contents/Library/SystemExtensions/`.
This is the location `kernelmanagerd` looks at when the host app
calls `OSSystemExtensionRequest.activationRequest`.

**Bundle identifiers:**

| Component | Bundle ID |
|---|---|
| Host app | `com.comprador.app` |
| Helper LaunchDaemon | `com.comprador.helper` |
| **Dext (new)** | `com.comprador.app.USBDriver` |

Apple requires the dext bundle ID to nest under the host app's bundle
ID for activation to succeed.

---

## Match dictionary

The dext personality (declared in `Info.plist` under
`IOKitPersonalities`) matches USB interfaces, not whole devices, so
that we displace `USBImaging` at the *interface* level without
interfering with the device's other interfaces (e.g. ADB on class FF
or serial on class 02).

Initial scope (Milestone 1): **class 6 (USB Imaging Class) interfaces.**

```xml
<key>IOKitPersonalities</key>
<dict>
    <key>CompradorUSBImagingDriver</key>
    <dict>
        <key>CFBundleIdentifier</key>
        <string>com.comprador.app.USBDriver</string>

        <key>IOClass</key>
        <string>IOUserService</string>

        <key>IOUserClass</key>
        <string>CompradorUSBImagingDriver</string>

        <key>IOProviderClass</key>
        <string>IOUSBHostInterface</string>

        <!-- USB Imaging Class — matches PTP/MTP class-6 interfaces.
             Vendor-specific class FF (Android MTP) is Milestone 3. -->
        <key>bInterfaceClass</key>
        <integer>6</integer>

        <!-- Imaging subclass 1 = Still Image Capture Device.
             This is what every PTP/MTP device declares. -->
        <key>bInterfaceSubClass</key>
        <integer>1</integer>

        <!-- Probe score: must beat USBImaging's score.
             USBImaging (in /System/Library/Extensions/IOUSBFamily.kext)
             scores ~80000. We use 90000 to displace it. Apple's
             documented ceiling for third-party drivers is 100000. -->
        <key>IOProbeScore</key>
        <integer>90000</integer>

        <!-- Required by DriverKit to load as user-space driver. -->
        <key>IOUserServerName</key>
        <string>com.comprador.app.USBDriver</string>
    </dict>
</dict>
```

**Why class 6 / subclass 1 specifically:** every PTP camera and every
phone in MTP mode declares this. Bare `bInterfaceClass=6` with no
subclass filter would match printer-imaging too, which we don't want.

**Vendor-specific class FF (Android MTP):** deferred to Milestone 3.
Class FF doesn't trigger the kernel `USBImaging` claim, so the
first-plug bug doesn't apply there. The dext should still own these
in Milestone 3 for consistency, but it's lower priority.

---

## IPC contract — dext ↔ host

The dext exposes a single user client (`IOUserClient` subclass) with
a small command set. The Go bridge talks to the user client through
the Swift app's `BridgeProcess.swift` plumbing — the dext does *not*
talk directly to the Go binary. Routing:

```
Go bridge ─▶ Unix socket ─▶ Swift app ─▶ IOUserClient ─▶ dext ─▶ device
```

The Unix socket is hosted by the Swift app (one socket per active
device), and the bridge speaks a small request/response protocol
over it. Why route through Swift instead of giving the bridge direct
IOUserClient access:

1. `IOServiceOpen` requires entitlements that are easier to scope on
   the Swift app than on a Go binary.
2. The Swift app already manages device lifecycle (`DeviceWatcher`,
   `BridgeProcess`); funneling IPC through it keeps that ownership.
3. Sandboxing — the Go bridge stays on its current minimal entitlement
   set (`com.apple.security.device.usb` for fallback libusb on
   non-class-6 devices); the dext-talking code is in Swift.

### IOUserClient method table

```
selector  name                    in                          out
0         open                    locationID:UInt32           token:UInt64
1         close                   token:UInt64                ─
2         getDeviceInfo           token:UInt64                vid:UInt16, pid:UInt16,
                                                              endpoints:[Endpoint]
3         bulkRead                token:UInt64,               data:[UInt8],
                                  endpoint:UInt8,             actualLen:UInt32
                                  maxLen:UInt32,
                                  timeoutMs:UInt32
4         bulkWrite               token:UInt64,               actualLen:UInt32
                                  endpoint:UInt8,
                                  data:[UInt8],
                                  timeoutMs:UInt32
5         interruptRead           token:UInt64,               data:[UInt8],
                                  endpoint:UInt8,             actualLen:UInt32
                                  maxLen:UInt32,
                                  timeoutMs:UInt32
6         resetPipe               token:UInt64, endpoint:UInt8 ─
```

`locationID` is the IOKit USB location ID of the *device* (not
interface). The dext maps that to the matched interface internally.

**Why `open` returns a token rather than a kernel handle directly:**
the host app may reconnect across restarts; tokens are opaque and
revoked on dext detach.

### Bridge-side wire format (Unix socket)

The Swift app exposes a Unix domain socket at
`/tmp/comprador.<port>.sock` (where `<port>` matches the bridge's
WebDAV port — already known to the app). The bridge speaks
length-prefixed framing:

```
┌────────────┬──────┬──────────────────┐
│ length     │ op   │ payload          │
│ uint32 LE  │ u8   │ <length-1 bytes> │
└────────────┴──────┴──────────────────┘
```

| op   | name           | payload                              | reply payload                           |
|------|----------------|--------------------------------------|-----------------------------------------|
| 0x01 | OPEN           | (none — device is implicit)          | u8 status, u64 token                    |
| 0x02 | CLOSE          | u64 token                            | u8 status                               |
| 0x03 | DEVICE_INFO    | u64 token                            | u8 status, u16 vid, u16 pid, n endpoints|
| 0x04 | BULK_READ      | u64 token, u8 ep, u32 maxLen, u32 ms | u8 status, u32 actualLen, bytes         |
| 0x05 | BULK_WRITE     | u64 token, u8 ep, u32 ms, bytes      | u8 status, u32 actualLen                |
| 0x06 | INTERRUPT_READ | u64 token, u8 ep, u32 maxLen, u32 ms | u8 status, u32 actualLen, bytes         |
| 0x07 | RESET_PIPE     | u64 token, u8 ep                     | u8 status                               |

Status byte mirrors `IOReturn`-like codes (0 = success, non-zero =
error class). The bridge doesn't need to interpret every value — it
maps non-zero to libmtp transport errors.

---

## Replacing libmtp's USB transport

This is the open architectural question for **Milestone 2**. Three
options, in increasing scope:

### Option A — patched libmtp with custom USB backend

Fork libmtp, replace `src/libusb1-glue.c`'s `LIBUSB_*` calls with
calls to a new `comprador-glue.c` that talks to the Unix socket.
Vendor the patched libmtp into `bridge/cvendor/` and link statically.

**Pro:** preserves all of libmtp's PTP protocol logic; only the
transport layer changes.

**Con:** maintaining a libmtp fork. Upstream rarely changes, so this
isn't bad in practice. Need to sync against future libmtp releases.

### Option B — patched libusb with custom macOS backend

Fork libusb, replace `darwin_usb.c`'s IOKit calls with calls to the
Unix socket. This is invisible to libmtp — it still calls
`libusb_bulk_transfer`, but the implementation routes through the
dext.

**Pro:** no libmtp fork; libusb already abstracts the platform
backend.

**Con:** libusb's macOS backend is more complex than libmtp's USB
glue (handles isochronous transfers, hotplug events, multiple
contexts), and most of that complexity isn't relevant. We'd be
maintaining a complex fork to ignore most of it.

### Option C — replace libmtp entirely with Go-native PTP

Reimplement the PTP/MTP protocol in Go directly on top of the dext
IPC. Roughly 2000–3000 lines of Go translating libmtp's
`src/libmtp.c` and `src/ptp.c`.

**Pro:** no C dependency at all; dropping cgo simplifies the build.

**Con:** large reimplementation; libmtp's quirks for specific phone
models (the `mtp-extensions.c` table) would have to be ported.

**Decision (tentative):** Option A. Smallest patch surface for the
biggest win. Revisit if libmtp upstream becomes too active to keep
syncing, or if the patched USB backend turns out to leak abstractions
that make the bridge messy.

The full call graph that needs replacing in libmtp:

```
LIBMTP_*  (libmtp.c)
  └─▶ ptp_usb_*  (libusb-glue.c)        ← REPLACE these
        └─▶ libusb_bulk_transfer        ← with calls to comprador IPC
```

A scratch sketch of the replacement:

```c
// bridge/cvendor/libmtp-comprador/src/comprador-glue.c
int ptp_usb_sendreq(PTPParams *params, PTPContainer *req) {
    // Serialize req to wire format
    // Call into Go via cgo callback that hits Unix socket
    return comprador_bulk_write(params->session_id, ep_out, buf, len);
}
```

The `params->session_id` comes from a new field we add at session
open time; it carries the IPC token returned by the OPEN op.

---

## Activation flow in the host app

```
┌─────────────────────────────────────────────────────────────┐
│  AppDelegate.applicationDidFinishLaunching                   │
│        │                                                     │
│        ▼                                                     │
│  DextActivator.requestActivation()                           │
│        │                                                     │
│        ▼                                                     │
│  OSSystemExtensionRequest                                    │
│   .activationRequest(forExtensionWithIdentifier:             │
│     "com.comprador.app.USBDriver")                           │
│        │                                                     │
│        ▼                                                     │
│  ┌──────────────────────────────────┐                        │
│  │ macOS: shows Privacy & Security  │                        │
│  │ banner "Allow Comprador to       │                        │
│  │ install a system extension?"     │                        │
│  └──────────────────────────────────┘                        │
│        │ user clicks Allow                                   │
│        ▼                                                     │
│  Delegate.didFinishWithResult(.completed)                    │
│        │                                                     │
│        ▼                                                     │
│  AppState transitions: needsDext → ready                     │
│  (next plug attempt routes through the dext path)            │
└─────────────────────────────────────────────────────────────┘
```

If activation fails (entitlement missing on the build, or user
declines), the app falls back to the existing libusb path with the
manual-replug recovery. This means **the dext is opt-in for the user
and additive for the codebase**: nothing existing breaks, nothing
existing depends on the dext succeeding.

New Swift file: `MenuBarApp/Sources/DextActivator.swift` (Dexter
territory; Mercer should not edit without coordination).

---

## Welcome / first-launch flow integration

**Status:** sketch only. None of this is implemented yet; the existing
[WelcomeWindow.swift](../MenuBarApp/Sources/WelcomeWindow.swift) ships
unchanged so we don't introduce dext-related cruft into versions
released before Apple grants the entitlement.

When the entitlement lands and `DextActivator.swift` is wired up, the
Welcome window's Setup section needs a second card. The existing card
covers "Start at login"; the new card covers "Allow the system
extension." Both follow the same visual pattern (icon, title,
explanation, status indicator, action button).

### State machine for the dext card

```
┌────────────────────────────────────────────────────────────┐
│  not-applicable                                            │
│   • Build doesn't include a dext (entitlement not granted, │
│     or ad-hoc signed dev build).                           │
│   • Card is hidden entirely.                               │
└────────────────────────────────────────────────────────────┘
                       │
                       ▼ (production build with dext bundled)
┌────────────────────────────────────────────────────────────┐
│  not-requested                                             │
│   • Card visible. Title: "Approve the USB driver".         │
│   • Action: button "Install".                              │
│   • Click → DextActivator.requestActivation()              │
└────────────────────────────────────────────────────────────┘
                       │
                       ▼
┌────────────────────────────────────────────────────────────┐
│  pending-user-approval                                     │
│   • macOS shows the system banner; user must click         │
│     "Allow" in Privacy & Security.                         │
│   • Card shows spinner + helper text:                      │
│     "Open System Settings → Privacy & Security and click   │
│     Allow next to Comprador."                              │
│   • Secondary button: "Open System Settings"               │
│     (deep-link via x-apple.systempreferences:               │
│     com.apple.preference.security?Privacy)                 │
└────────────────────────────────────────────────────────────┘
                       │ delegate fires .completed         │ delegate fires .failed
                       ▼                                    ▼
┌──────────────────────────┐                  ┌──────────────────────────────┐
│  installed               │                  │  failed                       │
│   • Green checkmark.     │                  │   • Red ⚠. Show error reason. │
│   • Status: "Installed". │                  │   • "Try Again" button.       │
└──────────────────────────┘                  └──────────────────────────────┘
```

The `not-applicable` branch is what keeps the welcome flow clean for
ad-hoc dev builds (`make app-swiftc`) — the card is *absent*, not
disabled. That's important: the existing welcome window stays visually
identical for builds that don't ship the dext.

### Sketch — additions to WelcomeViewModel

```swift
// LATENT — does not exist in the codebase yet. Implement when
// DextActivator lands.

enum DextStatus {
    case notApplicable          // no dext bundled in this build
    case notRequested            // bundled but never asked the user
    case pendingUserApproval     // request in flight, waiting on Allow
    case installed
    case failed(message: String)
}

extension WelcomeViewModel {
    @Published var dextStatus: DextStatus = .notApplicable

    func refreshDext() {
        // Read OSSystemExtensionManager state — populates dextStatus.
        // Hide the card entirely if dextStatus == .notApplicable.
    }

    func requestDextInstall() {
        // Calls into DextActivator; transitions to .pendingUserApproval.
        // Subscribes to delegate callbacks for .installed / .failed.
    }

    func openSystemExtensionSettings() {
        let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy")!
        NSWorkspace.shared.open(url)
    }
}
```

### Sketch — additions to the Setup section's view body

```swift
// LATENT — implement after DextActivator exists.

if viewModel.dextStatus != .notApplicable {
    HStack(alignment: .top, spacing: 12) {
        Image(systemName: dextStatusIcon)
            .foregroundStyle(dextStatusTint)
            .font(.title3)
            .padding(.top, 2)

        VStack(alignment: .leading, spacing: 2) {
            Text("Allow the USB driver").font(.body)
            Text(dextStatusBlurb)
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }

        Spacer(minLength: 0)

        switch viewModel.dextStatus {
        case .notRequested:
            Button("Install") { viewModel.requestDextInstall() }
        case .pendingUserApproval:
            Button("Open System Settings") {
                viewModel.openSystemExtensionSettings()
            }
        case .installed:
            Text("Installed").font(.caption).foregroundStyle(.secondary)
        case .failed:
            Button("Try Again") { viewModel.requestDextInstall() }
        case .notApplicable:
            EmptyView() // unreachable per outer guard
        }
    }
    .padding(12)
    .background(
        RoundedRectangle(cornerRadius: 10)
            .fill(Color(nsColor: .controlBackgroundColor))
    )
}
```

`dextStatusBlurb` and friends are status-driven copy:

| State | Title-line copy |
|-------|------------------|
| `notRequested` | "macOS will ask permission once. After that, your phone or camera works the instant you plug it in." |
| `pendingUserApproval` | "Open System Settings → Privacy & Security and click Allow next to Comprador." |
| `installed` | "Comprador's USB driver is active." |
| `failed(msg)` | "Couldn't install the driver: \\(msg). Restarting your Mac sometimes helps." |

### Activation timing

Two reasonable triggers; pick one when implementing:

- **A — fire from "Get Started":** the welcome window's primary
  button calls `requestDextInstall()` *before* dismissing if status
  is `.notRequested`. User sees the dismissal happen, then the macOS
  approval banner. Pro: no extra click. Con: surprising banner.
- **B — fire from the card's "Install" button:** explicit, in-line
  with the rest of the setup flow. Pro: predictable; matches the
  "Enable" pattern of the login item card. Con: one more click.

Recommended: **B**. The login item card already trains the user to
click an Enable-style button per setting; the dext card matches that
pattern exactly. Symmetry beats clever.

### What this does NOT change

- `LoginItem` setup card — unchanged, same code path.
- `HelperClient` (the `/etc/hosts` privileged helper) — unchanged. It
  remains a separate, optional setup step accessible from the menu
  bar, not the welcome window.
- The "How to connect" card — unchanged. Plug-in instructions are
  the same whether or not the dext is present.

### Build-time conditional

Whether the dext card *can* show is decided at build time, not
runtime. The check is whether `Comprador.app/Contents/Library/SystemExtensions/com.comprador.app.USBDriver.dext`
exists in the running bundle:

```swift
private static var dextIsBundled: Bool {
    Bundle.main.url(
        forResource: "com.comprador.app.USBDriver",
        withExtension: "dext",
        subdirectory: "Library/SystemExtensions"
    ) != nil
}
```

Returns `false` for `make app-swiftc` (dev) builds; returns `true`
for `make app-with-dext` (production) builds. `WelcomeViewModel.refreshDext()`
short-circuits to `.notApplicable` if `dextIsBundled` is false, which
is what hides the card.

---

## Build pipeline

The dext **must** be built by Xcode. There's no `swiftc` or `clang`
incantation that produces a valid `.dext` — Xcode's build system
runs `dext_codesign` and applies the system-extension entitlements
in a way that ad-hoc signing cannot reproduce.

Mercer's `make app-swiftc` path stays clean. Two new targets:

```makefile
# Build the dext target out of the Xcode project.
dext:
	xcodebuild -project MenuBarApp/Comprador.xcodeproj \
	           -scheme USBDriver \
	           -configuration Release \
	           -destination 'generic/platform=macOS' \
	           build

# Full app + dext build for distribution. Requires real Developer ID
# signing (not ad-hoc) for the dext to load.
app-with-dext: bridge dext
	xcodebuild -project MenuBarApp/Comprador.xcodeproj \
	           -scheme Comprador \
	           -configuration Release \
	           build
	# BUNDLE_BRIDGE + BUNDLE_HELPER + copy dext into Library/SystemExtensions/
```

Notes for whoever wires this up (probably future-Dexter):

- The Xcode project needs a new target of type *Driver Extension*
  with template *DriverKit* → *USB Driver*.
- The host app's *Embed* phase must include the dext target so it
  ends up in `Contents/Library/SystemExtensions/`.
- `make app-swiftc` cannot ship the dext (ad-hoc signing won't load
  it), so it's effectively a "developer iteration without dext"
  build. That's fine — the dext is a separate dimension of testing.

---

## Sideloading during development

Until the entitlement is approved by Apple, the dext won't load on
any normal machine. There are two dev workarounds:

1. **`systemextensionsctl developer on`** + reboot. Lowers the
   Gatekeeper bar so unsigned/ad-hoc-signed dexts can load. Still
   requires SIP-relaxed mode (`csrutil enable --without-fs` or
   similar). This is what Apple's own dext sample code documents.
2. **Provisioning profile with explicit dev entitlements.** Possible
   only after Apple approves the entitlement request, so this isn't
   a workaround — it's the production path.

For Milestone 1 dev iteration, we'll use (1) on a single dev machine.
For any other tester, we wait for (2).

---

## Testing strategy

**Milestone 1 success criterion** (from HANDOFF-DRIVERKIT.md):

> Plug phone in with Comprador running. Within one second, log shows
> the dext matched and ptpcamerad does NOT have the device (verify
> with `lsof | grep ptpcamerad`).

Test rig:

```bash
# Verify dext matched the device
log show --last 10s --predicate 'subsystem == "com.comprador.app.USBDriver"'

# Verify ptpcamerad does NOT have it
lsof -p $(pgrep ptpcamerad) | grep -i usb
# (expected: no class-6 USB device entries)

# Verify our dext does have it
ioreg -p IOUSB -l | grep -A 5 "Comprador"
```

**Milestone 2 success criterion:**

```bash
# Phone plugged in for 30+ seconds, ptpcamerad has touched it
sleep 30
# Mount and transfer
make run-swiftc
# In another terminal:
cp ~/Downloads/100mb-test.bin /Volumes/Pixel-6/sdcard/test.bin
shasum ~/Downloads/100mb-test.bin
shasum /Volumes/Pixel-6/sdcard/test.bin
# Hashes match.
```

---

## Open questions

These are flagged as `<!-- dexter-q: -->` in code where relevant.

1. **Probe score 90000 — is it enough?** USBImaging's actual score on
   recent macOS is undocumented. Verify on first dext load with
   `ioreg -l` showing match scores.
2. **Token persistence across dext crashes.** If the dext crashes,
   `kernelmanagerd` restarts it, but the host's tokens are invalid.
   Need a reconnect path in `BridgeProcess.swift`.
3. **Multi-device.** The match dictionary will fire once per
   matching interface; the dext needs to track multiple `(locationID
   → interface)` pairs. Punted to Milestone 3.
4. **libmtp fork licensing.** libmtp is LGPL-2.1-or-later. A patched
   fork must keep the license and offer source — easy because
   Comprador is GPL-3.0-or-later (compatible direction). Patch goes
   in `bridge/cvendor/libmtp-comprador/` with a clear PATCH-NOTES.md.

---

## Implementation order (revised from HANDOFF-DRIVERKIT.md)

The handoff doc gave milestones; this is the file-by-file order
within each milestone, so future-Dexter can pick up cleanly.

### Milestone 1 — proof of life

Pre-req: both entitlements approved by Apple.

1. Scaffold dext target in Xcode (UI-driven; commit the resulting
   project changes in a single commit).
2. `USBDriver/CompradorUSBImagingDriver.iig` — interface declaration.
3. `USBDriver/CompradorUSBImagingDriver.cpp` — IOService subclass,
   `Start()` / `Stop()` / `NewUserClient()`.
4. `USBDriver/CompradorUSBImagingClient.cpp` — IOUserClient subclass
   with the method table above; only `open`, `close`, `getDeviceInfo`
   wired up. Bulk operations stub out with `kIOReturnUnsupported`.
5. `MenuBarApp/Sources/DextActivator.swift` — activation request
   plumbing, delegate, status reporting to the menu UI.
6. Smoke test: a debug menu item that calls `getDeviceInfo` and
   logs the result.

### Milestone 2 — bulk transfer pipe

7. Wire up bulk read/write/interrupt in the IOUserClient.
8. Unix-socket bridge in Swift (`SocketServer.swift`) that exposes
   the wire protocol to the Go bridge.
9. Vendor patched libmtp at `bridge/cvendor/libmtp-comprador/`.
10. Switch `bridge/mtp/binding.go` cgo flags to link against the
    patched libmtp, conditionally on a build tag (so we can keep
    `make app-swiftc` building against system libmtp for non-dext
    iterations).
11. End-to-end transfer test against Pixel 6 in MTP mode after
    `ptpcamerad` has touched it.

### Milestone 3 — coverage

12. Add class FF match personality (Android vendor MTP).
13. Multi-device support in the dext + IPC layer.
14. Test against Canon/Nikon/Sony PTP cameras.

---

*This document is owned by Dexter; Mercer changes welcome via
`<!-- mercer: -->` comments or a PR with `dexter/` review.*
