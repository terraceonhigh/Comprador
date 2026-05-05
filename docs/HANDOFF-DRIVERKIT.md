# Handoff: DriverKit extension for first-plug claim

**From:** Claude Mercer (project work through 2026-05-04)
**To:** Claude Dexter (DriverKit specialist)
**Subject:** The one path forward to claim a USB MTP/PTP interface that
ptpcamerad already owns. Multi-day work; do not start without reading
[TODO.md](../TODO.md) and the closing commits referenced below.

> **Status update — 2026-05-04 (Dexter active):** Apple Developer
> Program enrollment confirmed (Team ID `5875SC35WL`, individual).
> The DriverKit USB Transport entitlement request is filed with
> Apple (text in [ENTITLEMENT-REQUEST.md](ENTITLEMENT-REQUEST.md));
> the System Extension Install capability is **self-service** on the
> App ID page, not a separate Apple review (corrected mid-session
> after Apple's portal made it obvious). Dext architecture committed
> in [DEXT-DESIGN.md](DEXT-DESIGN.md). Empty
> [USBDriver/](../USBDriver) directory reserved for the dext target
> with a README. Host-side entitlement
> `com.apple.developer.system-extension.install` added to
> [Comprador.entitlements](../MenuBarApp/Comprador.entitlements);
> `make app-swiftc` still builds cleanly (verified). Code work past
> the design doc is gated on Apple approving the DriverKit USB
> Transport entitlement (2–6 weeks).

---

## The problem in one paragraph

Comprador mounts an Android phone (or PTP camera) as a Finder volume by
spawning a Go bridge that calls `libusb_claim_interface` on the device's
class-6 USB Imaging Class interface. This works **only when the bridge
spawns within ~5 seconds of the phone's USB enumeration** — i.e., when
the user starts Comprador *before* plugging in. If the phone is already
plugged in when Comprador starts, `ptpcamerad` has had time to take
exclusive access of the device, and `libusb_claim_interface` fails
forever with `LIBUSB_ERROR_ACCESS`. The only userland recovery is a
physical unplug + replug, which we wire into the failure notification
and the auto-retry path.

This handoff is for the work to make first-plug-after-app-start
**actually work**, without telling users to physically replug.

---

## Dead ends — do not re-walk

Recorded in [TODO.md](../TODO.md) under "First-plug failure is unwinnable
from any non-SIP-disabled path", with full diagnosis. Briefly:

- **`killall -9 ptpcamerad`** — launchd respawns within ~60ms (it's a
  Mach-service-on-demand LaunchAgent). The bridge can't win the race.
- **`IOUSBDeviceInterface500.USBDeviceOpenSeize`** — returns
  `kIOReturnExclusiveAccess (0xE00002C5)`. IOKit refuses to evict an
  exclusive holder from userspace.
- **`libusb_detach_kernel_driver`** — returns "Invalid argument".
  macOS doesn't support userspace driver detach for the USB Imaging
  Class.
- **`libusb_reset_device`** — returns `LIBUSB_ERROR_NO_DEVICE`. The
  macOS reset path requires seized ownership we can't get.
- **`launchctl bootout gui/<UID>/com.apple.ptpcamerad`** — fails with
  "Operation not permitted while System Integrity Protection is
  engaged". Even root cannot bootout Apple's
  `/System/Library/LaunchAgents` services with SIP on. We tried
  `user/<UID>` first and got "service not found in domain"; the
  daemon is in `gui/<UID>` but SIP guards it there.

The relevant log evidence and exit codes are quoted in commit messages
[3c238f4](https://github.com/terraceonhigh/Comprador/commit/3c238f4)
("Stop fighting SIP") and [06b356d](https://github.com/terraceonhigh/Comprador/commit/06b356d).

---

## Why DriverKit

DriverKit is Apple's user-space replacement for kernel extensions.
A DriverKit extension (dext) ships inside the app bundle, gets loaded
by `kernelmanagerd` when the app is launched, and runs in a sandboxed
userspace process *with kernel-equivalent IOKit privileges* for the
specific device family it claims.

**The relevant capability:** a USB DriverKit dext can match against a
USB device or interface and become its driver, *displacing the
in-kernel USBImaging match*. From there, it forwards data to the
hosting app via IPC. No kext, no SIP-off, no Apple ID password prompt
beyond the one-time "Allow system extension" approval that user has
already gotten used to (similar to how Little Snitch and DisplayLink
work).

The code path we want:

1. Comprador.app is installed.
2. On first launch, the app requests activation of the bundled dext.
3. macOS prompts user once: "Allow Comprador to install a system
   extension?"
4. User clicks Allow in System Settings → Privacy & Security.
5. From now on, when the user plugs in a phone, the dext matches
   *before* USBImaging does (because dext match scores can be tuned
   higher), claims the interface, and exposes a userspace endpoint
   for the bridge to talk to instead of libusb.
6. The Go bridge no longer calls libusb directly for that device —
   it calls into the dext via the IPC channel.

Net effect: `ptpcamerad` never gets the device, because the dext
got there first. First-plug-after-app-start works.

---

## Required reading before writing any code

In order:

1. **[TODO.md](../TODO.md)** — the entire "Known friction points"
   section, especially the long entry about the SIP wall. That's the
   problem definition.
2. **[CLAUDE.md](../CLAUDE.md)** — sections "Architecture Decision
   Record" and "Component 1: Go WebDAV Bridge". Understand the bridge's
   place in the system before touching it.
3. **[bridge/mtp/binding.go](../bridge/mtp/binding.go)** —
   `DetectDevice()` and the libmtp open path. This is what dext IPC
   will replace for the device-talking case.
4. **[MenuBarApp/Sources/BridgeProcess.swift](../MenuBarApp/Sources/BridgeProcess.swift)**
   — `start()` and `killCompetingProcesses()`. The seize attempt and
   the killall belong here; you'll add dext activation here too.
5. **[MenuBarApp/Sources/USBSeizer.swift](../MenuBarApp/Sources/USBSeizer.swift)**
   — already does the IOKit COM-style plugin-interface dance for
   `IOUSBDeviceInterface500`. Useful prior art for IOKit-from-Swift
   patterns; the bridging-header / CFUUID-from-bytes tricks transfer
   to dext communication.

Apple documentation worth grokking before writing:
- `DriverKit` framework reference (`IOUserUSBHostDevice`,
  `IOUserUSBHostInterface`)
- "USB DriverKit" sample code from Apple
- "Communicating Between a DriverKit Extension and a Client App" guide
- WWDC 2020 "Bring your driver to iPad with DriverKit" (still relevant
  for macOS USB dexts)

---

## Scope and milestones

### Milestone 1 — proof of life

Bare-bones dext that:
- Matches `IOUSBHostInterface` for class 6 (PTP) devices.
- Claims the interface (which beats USBImaging to it).
- Logs a single "claimed" message.
- Exposes a single IPC method `getDeviceID()` that returns the
  vendor/product IDs.

App-side: activation + a smoke test that calls `getDeviceID()` and
prints the result. No real MTP yet.

**Success criterion:** Plug phone in with Comprador running. Within
one second, log shows the dext matched and ptpcamerad does NOT have
the device (verify with `lsof | grep ptpcamerad`).

Estimated work: 3–5 days of dext + entitlements + signing flailing.
This milestone is a flag-planting exercise; the actual MTP forwarding
comes next.

### Milestone 2 — bulk transfer pipe

Replace the libusb claim path inside `bridge/mtp/binding.go` with
calls to the dext (via the IPC channel established in Milestone 1).
The dext does:
- Bulk-IN read (data from device)
- Bulk-OUT write (commands to device)
- Interrupt-IN read (events from device)

Bridge-side: Go IPC client to talk to the dext. Could be a Unix socket
the dext exposes, or `IOUserClient` connection passed through Swift
glue, depending on what's cleanest.

The libmtp PTP-protocol layer stays the same — only the transport
changes. So most of the bridge code is untouched.

**Success criterion:** Mount a Pixel 6 that's been plugged in for >30
seconds, in the same login session where ptpcamerad has already
opened it. Browse files, transfer a 100MB file in each direction,
verify checksums.

### Milestone 3 — the rest of the device universe

- Camera class 6 (PTP) devices (Canon, Nikon, Sony, Fuji)
- Android vendor-specific MTP interfaces (class FF — these don't
  hit the kernel-claim issue but the dext should still own them
  for consistency)
- Multiple devices simultaneously (deferred from current product;
  may stay deferred)

---

## Distribution constraints

- **Notarization is required.** Dexts must be notarized by Apple to
  load. This means Apple Developer Program enrollment is a
  prerequisite for *anyone* installing a build. (Confirmed
  2026-05-04: Team ID `5875SC35WL`.)
- **System Extension Install capability** is required on the host
  app (`com.apple.developer.system-extension.install`). This is a
  self-service toggle on the App ID at developer.apple.com →
  Identifiers — no Apple review, just tick the box when registering
  the App ID. Earlier drafts of this doc described it as a separate
  Apple review request; that was wrong.
- **DriverKit USB Transport entitlement** is required on the dext
  (`com.apple.developer.driverkit.transport.usb`). Apple
  individually approves these; expect 2–6 weeks turnaround. Worth
  filing the request before writing much code so the timer runs in
  parallel. The form takes a single VID per submission, but
  Comprador needs ~15 Android vendors plus camera vendors — see
  [ENTITLEMENT-REQUEST.md](ENTITLEMENT-REQUEST.md) for the
  multi-vendor handling.
- The dext bundles inside `Comprador.app/Contents/Library/SystemExtensions/`
  and gets activated via `OSSystemExtensionRequest.activationRequest`.

---

## Coordination with Claude Mercer

The work crosses subsystems Mercer has been touching, so:

- **Don't change the bridge's PORT/HOST/DEVICE stdout protocol** in
  [BridgeProcess.swift](../MenuBarApp/Sources/BridgeProcess.swift).
  AppDelegate parses these.
- **Don't break `make app-swiftc`** as a build path. CI and Mercer's
  iteration loop depend on it. The dext build will need its own
  target (likely `make dext` and `make app-swiftc-with-dext`) but
  the existing Swift-only path stays clean.
- **Coordinate on entitlements file** — both dext and host app gain
  new entitlements. Expand [Comprador.entitlements](../MenuBarApp/Comprador.entitlements)
  rather than creating a parallel file.
- **License: still GPL-3.0-or-later.** The dext is part of Comprador
  and inherits the project license. SPDX header on every new file:
  `// SPDX-License-Identifier: GPL-3.0-or-later`. Add Apple's
  DriverKit headers to [NOTICES.md](../NOTICES.md) once you know
  what's bundled.
- **Commit messages: keep the long-form why-not-just-what style**
  Mercer's been using — see commit
  [4af926b](https://github.com/terraceonhigh/Comprador/commit/4af926b)
  for an example. Future-Claude will read these to onboard.
- **Co-author tag:** sign your commits with
  `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
  so the human collaborator can grep the history by author for
  AI-touched commits.

When you need Mercer's hands on something (UX copy in the welcome
window once the dext install flow is in place, the README "What
Works" section, etc.), leave a `<!-- mercer: -->` HTML comment in
the relevant file with a one-liner. Mercer does the same in reverse
when it's a dext concern.

If we both end up needing to land changes in the same file in
overlapping windows, branch — `mercer/<topic>` and `dexter/<topic>`.

---

## Pre-flight checklist for Claude Dexter

Before starting Milestone 1, confirm with the human collaborator:

- [x] Apple Developer Program enrollment is active (need the team ID
      to scaffold the dext bundle). **Done 2026-05-04 — Team ID
      `5875SC35WL`, individual enrollment.**
- [ ] DriverKit USB Transport entitlement has been requested. (Apple
      may take weeks; request before writing code.) **Drafted in
      [ENTITLEMENT-REQUEST.md](ENTITLEMENT-REQUEST.md); user is
      filing the request with Apple — update the status table in
      that file when filed and again when Apple responds.**
- [x] User has read this handoff and the linked TODO.md entry, and
      agrees with the scope before you spend a week on Milestone 1.
- [x] User wants the dext as a system extension activated via
      `SMAppService.daemon` / `OSSystemExtensionRequest`, *not* as
      a plain helper. They are different mechanisms; this one
      requires Apple approval but gives the kernel access we need.

If any of those are blockers, say so before starting. We've been
honest with the human all session about scope realities, and
Milestone 1 alone is not worth starting on a hopeful timeline if the
entitlements aren't lined up.

Good luck, Dexter.

— Mercer
