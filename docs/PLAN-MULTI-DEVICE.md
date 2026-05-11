# Plan — multi-device support

**Status — 2026-05-10:** scope and design notes for supporting N
phones (or one phone + one camera, etc.) mounted simultaneously,
each as its own Finder Locations sidebar entry. Decision the
Architect has already made: **N devices → N sidebar entries, all
active at once.** Not switch-on-click.

## Why this is easier than the references suggested

The earlier forensics on OpenMTP and SwiftMTP turned up a
surprise: **neither actually supports concurrent multi-device
sessions, despite their marketing.** Both reuse a `go-mtpx`
backend with a singleton `container.dev`. The flow is
"N-detected, 1-active, switch-on-click" — full Initialize/Dispose
on every device change. SwiftMTP's UI shows N device sections in
its sidebar, but only one device is browseable at a time.

What this means for Comprador:

- **There is no concurrent-session prior art to copy** from those
  two projects.
- **Comprador's architecture is structurally better-suited for
  true N-simultaneous** than the references' in-process-cgo
  designs. We run the MTP backend as a *separate subprocess
  (bridge)* per device, each with its own libmtp session, its own
  NFS port, and its own mount path. The Swift menu-bar app is the
  only layer that assumes singleton today, and the refactor is
  bounded.
- The right model is one we'd have arrived at independently:
  per-device subprocess + per-device mount, orchestrated from a
  dictionary-keyed manager in Swift.

We borrow the *UI shape* from SwiftMTP (N sidebar/menu entries,
per-device capacity info) and ignore their session model.

## Where the singleton assumption lives today

[MenuBarApp/Sources/AppDelegate.swift:1–20](../MenuBarApp/Sources/AppDelegate.swift):

```swift
class AppDelegate: NSObject, NSApplicationDelegate {
    private var bridge: BridgeProcess?
    private var mountManager = MountManager()
    private var resumeCompanion: ResumeCompanion?

    private var connectedDevice: USBDevice?
    private var isConnecting = false
    private var pendingAttach: USBDevice?
    private var connectStatus: String = ""
    private var connectStartedAt: Date?
    private var connectTimer: Timer?
    private weak var connectingStatusItem: NSMenuItem?
    private var registeredHostname: String?
```

Every one of those `private var`s is a singleton — there's at most
one of each in the whole app. The menu's
[rebuildMenu()](../MenuBarApp/Sources/AppDelegate.swift) is built
around a single `connectedDevice`. The reattach-during-unmount
race fix
([MISTAKES.md 19a](MISTAKES.md))
gates on a single `pendingAttach`.

[MountManager](../MenuBarApp/Sources/MountManager.swift) has
`private(set) var mountPath: URL?` — singleton.

[BridgeProcess](../MenuBarApp/Sources/BridgeProcess.swift) is a
*class* (so instantiable N times) but only one instance is ever
created.

[DeviceWatcher](../MenuBarApp/Sources/DeviceWatcher.swift) already
speaks per-device — it emits attach/detach events for each USB
device individually. **No change needed here.** This is the
unsung hero of the refactor; the event source is already correctly
shaped.

## Target shape

Introduce a new type encapsulating the per-device state:

```swift
class DeviceSession {
    let device: USBDevice
    let bridge: BridgeProcess           // one per session, its own port
    let mountManager: MountManager      // one per session, its own mount path
    var isConnecting: Bool = false
    var pendingAttach: USBDevice?       // queued reattach (19a)
    var connectStatus: String = ""
    var connectStartedAt: Date?
    var connectTimer: Timer?
    var registeredHostname: String?
    // ... whatever else is currently a singleton field on AppDelegate
}
```

`AppDelegate` replaces its singleton fields with:

```swift
private var sessions: [DeviceID: DeviceSession] = [:]
private var resumeCompanion: ResumeCompanion?  // can stay singleton if scoped to host
```

Where `DeviceID` is the USB Location ID (stable across reattach of
the *same* device on the *same* port) or the device's serial
number (stable across ports but requires an MTP query). Best
choice: **Location ID** as the primary key, with a fallback to
serial for cross-port stability if we discover Location ID is too
brittle in practice.

## Concrete changes

### 1. `DeviceSession` type

New file `MenuBarApp/Sources/DeviceSession.swift`. Holds
everything currently in `AppDelegate`'s per-device-state region.
Constructor takes a `USBDevice` and creates fresh `BridgeProcess`
and `MountManager` instances.

### 2. `AppDelegate.sessions` dictionary

Replace singleton state with `[DeviceID: DeviceSession]`. Every
event handler:

- `handleDeviceAttached(_ device: USBDevice)` — look up session by
  device ID. If missing, create. If present (reattach race), update
  in place.
- `handleDeviceDetached(_ device: USBDevice)` — look up, tear down,
  remove from dict.
- Menu rebuild — iterate over `sessions.values`, render one section
  per session.

### 3. Per-device `BridgeProcess` and `MountManager`

Each `DeviceSession` owns its own. Two devices means two bridge
processes on two ports (each picks `127.0.0.1:0`), two NFS mounts
at two paths.

`MountManager` already produces a unique path per mount (via the
device's `.local` hostname); just instantiate one per session.

`BridgeProcess` needs to be told *which device* to claim. Today
[BridgeProcess.start](../MenuBarApp/Sources/BridgeProcess.swift)
accepts `seizeForVendor` / `seizeForProduct` parameters that
target the first matching device. For multi-device, we need to
target a *specific* device — likely by USB Location ID. This is
the largest concrete code change in the plan; see §6.

### 4. Menu UX

Following the Architect's instinct, **N sidebar items for N
devices**. The macOS menu bar (NSStatusItem) shows a single icon;
the menu it opens lists per-device sections. Each section has:

- Device name (header, disabled)
- "Show in Finder" → opens that device's mount
- "Eject [device name]" → unmounts only that device
- Connecting status / elapsed timer (if in connect phase)
- Separator before the next device

Plus a single global "Quit Comprador" at the bottom.

Icon state in the menu bar:

- **No devices:** `externaldrive` (idle)
- **Any device connecting, none mounted:** `externaldrive` animated
- **Any device mounted:** `externaldrive.fill` (the standard state)
- **Any device errored:** `externaldrive.badge.xmark` (priority
  over "any mounted" — surface the problem)

Borrows shape from SwiftMTP's
[SidebarView.swift:36–39](../../references/SwiftMTP/SwiftMTP/Views/SidebarView.swift)
which iterates `ForEach(manager.availableDevices) { device in
deviceSection(device) }`. Same pattern, our menu instead of their
sidebar.

### 5. Reattach race (19a) becomes per-device

The `pendingAttach` queue currently global; becomes per-session.
Each `DeviceSession` tracks its own pending reattach. A reattach
event for device A while device A is mid-unmount queues at
`sessions[A].pendingAttach`; device B is unaffected.

Cleanest: move the entire mount/unmount sequence into
`DeviceSession.connect()` / `DeviceSession.disconnect()` so the
race window is local to that session's state machine.

### 6. `BridgeProcess` per-device targeting

Today the bridge subprocess detects "the first MTP device on the
USB bus" via libmtp's auto-detect. With N devices that's
ambiguous. Two paths:

- **A. Pass Location ID via CLI flag.** New flag like
  `--device-loc-id=<id>`. Bridge filters libmtp's raw-device list
  to the matching one. ~30 lines in
  [bridge/main.go](../bridge/cmd/) and
  [bridge/mtp/binding.go](../bridge/mtp/binding.go).
- **B. Pass USB Vendor+Product+Serial.** Same approach, more
  brittle if two same-model phones are plugged in.

**Recommendation: A**, with B as fallback if Location ID turns out
not to be exposed cleanly through libmtp's raw-device list. Verify
with a quick spike against a two-phone plug-in before committing.

### 7. `ptpcamerad` seizure with N devices

The kill-and-claim race
([MISTAKES.md 19](MISTAKES.md))
currently kills `ptpcamerad` once globally. With N devices
plugging in near-simultaneously, the race window narrows but the
single global kill is still the right move — `ptpcamerad` is
process-wide, not per-device. The bridge's claim is per-device
(via libmtp), so each bridge process claims its own.

What we don't have today and will need: **serialization of the
bridge spawn during the initial-claim phase across multiple
devices.** If two phones plug in within 100 ms, two bridges race
to spawn, both `killall ptpcamerad`, both compete to claim
different interfaces. libmtp/libusb should handle the per-device
claim independently, but the `killall` storm is wasteful.

Mitigation: a `USBSeizer.shared` actor that batches
`killall` calls (one per 200 ms window) so back-to-back attaches
don't spam.

### 8. Memory pressure with N devices

[MISTAKES.md 8a](MISTAKES.md) documents the cgo-callback per-call
allocation issue: each multi-GiB transfer leaks ~one file-size's
worth of `VM_ALLOCATE` regions until the bridge dies. With N
devices, N transfers can be in flight, each leaking.

**Status (2026-05-11):** the cgo callback buffer-reuse fix
shipped on 2026-05-06 in commit `90fb7216`. The leak is closed
at the binding layer; each transfer now reuses a single
per-session buffer (~22 MiB) regardless of file size.
Empirical re-verification with a multi-GiB transfer + vmmap
read is the remaining follow-up but is not a hard gate.

The original framing in this section ("pre-condition for
shipping multi-device") is preserved for posterity; the
condition is met.

### 9. ResumeCompanion: shared or per-device?

[ResumeCompanion](../MenuBarApp/Sources/ResumeCompanion.swift)
is the Mac-side companion that completes a truncated WebDAV
upload by reading the source file directly when the bridge
signals truncation. It's NFS-pivot-deprecated for the v0.3+
mount path; remains in tree for legacy WebDAV. On the multi-device
NFS path it can stay singleton (it listens on its own port for
bridge callbacks; multiple bridges signal the same companion).
Or, simpler: don't carry it forward when we delete WebDAV
in v0.4.0.

## What to borrow

- **SwiftMTP's per-device section pattern**
  ([SidebarView.swift:36–39](../../references/SwiftMTP/SwiftMTP/Views/SidebarView.swift))
  — `ForEach(devices) { deviceSection($0) }`. We adapt to NSMenu.
- **SwiftMTP's connection-phase state machine** (KalamMTPManager
  has phases: `.disconnected → .connecting → .connected → .error`).
  Useful for per-device status display; map to our existing
  `isConnecting` flag + `connectStatus` string per session.
- **OpenMTP's device-changed detection by serial** — useful as
  fallback identity check when Location ID flakes
  (`verifyMtpSession` in
  [helpers.go:10–33](../../references/openmtp/ffi/kalam/native/helpers.go)).

## What *not* to borrow

- **Their singleton `container.dev`** — explicitly the thing we're
  doing differently.
- **Switch-on-click semantics** — we want all concurrent.
- **Their session locking** (`ErrorMtpLockExists`) — that's
  enforced inside a single-session backend. Our subprocesses are
  already isolated per device.
- **Marketing language that conflates detection with concurrent
  sessions** — be honest in README/CLAUDE.md that "multi-device"
  for Comprador means *actually concurrent*, unlike the references.

## Risks

1. **Location ID stability across reattach.** USB Location IDs are
   port-stable but not device-stable. Plug device into port A,
   unplug, plug into port B — different Location ID. The dict
   key would mismatch. Mitigation: also track serial number where
   available; use it for "same physical device, different port"
   detection.

2. **Reattach race × N**. The 19a race is bad enough with one
   device. With two devices flapping simultaneously, the
   sequencing gets gnarly. The per-session model contains it
   (each session's `pendingAttach` is independent), but worth
   stress-testing with a USB hub and a script.

3. **NFS port exhaustion.** Each bridge picks 127.0.0.1:0; not a
   real concern for any realistic N. Note for completeness.

4. **Memory** (covered in §8). cgo fix lands first.

5. **`killall ptpcamerad` thrashing** under rapid plug events.
   See §7.

6. **Two phones with identical names.** If both are unconfigured
   Pixel 6s, both advertise hostname `Pixel-6.local` — mDNS
   collision, NFS mount paths collide. Mitigation: append a
   disambiguator if registration sees a conflict. Same shape as
   the multi-storage name-collision handling in
   [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md) §5.

## Sequence

1. **(Prerequisite — DONE 2026-05-06, commit `90fb7216`)** The
   cgo callback buffer-reuse fix has landed. Multi-device is
   unblocked. Verification by `vmmap` retake on a multi-GiB
   transfer is the open follow-up but not a hard gate for
   refactor work below.
2. **Refactor: introduce `DeviceSession`** as a class
   encapsulating the current per-device state. Keep
   `AppDelegate`'s API surface unchanged externally; internally,
   `var session: DeviceSession?` instead of the loose fields.
   Verify single-device behavior is unchanged. This is a no-op
   refactor by design.
3. **Refactor: AppDelegate holds a dictionary** of
   `[DeviceID: DeviceSession]`, even if only ever populated to
   size 1. Menu iterates. Still single-device in practice; just
   the data structure is plural. Verify nothing regresses.
4. **Bridge per-device targeting** (§6 option A). New CLI flag,
   bridge filters. Test by hand-spawning two bridges on the
   command line against two different phones.
5. **Wire DeviceWatcher's per-device events to per-session
   handlers.** Two attached devices now produce two sessions.
   Test with two phones.
6. **Menu UX** — N sections per N sessions. Polish the icon
   state machine.
7. **Edge cases:** hostname collision (§risks 6), reattach-race
   × N (§risks 2), eject of one device while another is mid-
   transfer.

## Out of scope

- **Per-device welcome/onboarding UI.** Welcome shows once at
  app first launch, not per device.
- **Cross-device file move.** Two phones plugged in, drag from
  one to the other — Finder round-trips through the Mac, same as
  cross-storage drags. Fine; no special handling.
- **Quotas across devices.** Already handled per-mount by
  [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md). Each device's
  mount has its own FSStat numbers.
- **A device "favorites" or "pinning" feature.** YAGNI; the
  Architect plugs in what they plug in.
- **Bluetooth / wireless MTP devices.** Out per
  [CLAUDE.md](../CLAUDE.md) non-goals.

## Receipts

Comprador current singleton state (the refactor targets):

- [AppDelegate.swift:1–20](../MenuBarApp/Sources/AppDelegate.swift)
  — singleton fields enumeration.
- [BridgeProcess.swift:5–7](../MenuBarApp/Sources/BridgeProcess.swift)
  — single bridge instance.
- [MountManager.swift:12–14](../MenuBarApp/Sources/MountManager.swift)
  — `mountPath: URL?` singleton.
- [DeviceWatcher.swift](../MenuBarApp/Sources/DeviceWatcher.swift)
  — already per-device, no refactor needed.

Reference patterns (verified 2026-05-10 by Explore agents,
captured in [SWIFTMTP-NOTES.md](SWIFTMTP-NOTES.md) and
[OPENMTP-NOTES.md](OPENMTP-NOTES.md)):

- OpenMTP backend singleton (the thing we *don't* want):
  [structs.go:13–17](../../references/openmtp/ffi/kalam/native/structs.go),
  [kalam.go:19](../../references/openmtp/ffi/kalam/native/kalam.go).
- OpenMTP rejects multi-device-attached:
  [send_to_js/helpers.go](../../references/openmtp/ffi/kalam/native/send_to_js/helpers.go)
  `ErrorMultipleDevice`.
- SwiftMTP's switch-on-click implementation:
  [KalamMTPManager.swift:1067–1096](../../references/SwiftMTP/SwiftMTP/Services/KalamMTPManager.swift).
- SwiftMTP's sidebar iteration (the pattern we adapt):
  [SidebarView.swift:36–39](../../references/SwiftMTP/SwiftMTP/Views/SidebarView.swift).

Pre-condition tracked here:

- [TODO.md](../TODO.md) "cgo MTP callback: reuse buffer per
  session instead of allocating per call."
- [MISTAKES.md entry 8a](MISTAKES.md) — the cgo allocation
  receipt + analysis.

Sister plan:

- [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md) — independent;
  both can ship in either order.
