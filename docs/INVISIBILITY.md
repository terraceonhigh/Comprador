# INVISIBILITY — what would make Comprador feel like a thumb drive

> *Drafted 2026-05-17 in response to the architect's question
> "what could make ourselves that invisible?" after the
> three-step multi-device-race remediation arc.*
>
> The honest answer is that perfect invisibility isn't
> achievable — there's a hard ceiling set by macOS not shipping
> a native MTP driver and by Android phones not exposing USB
> Mass Storage. But the gap between us and that ceiling can
> shrink substantially. This document inventories the gaps,
> categorises them by what we can control, and ranks the
> high-leverage moves.

## The thumb-drive baseline

A USB Mass Storage thumb drive achieves invisibility in six
distinct ways:

1. **Plug-to-mounted in under a second.** Kernel sees USB Mass
   Storage class, mounts via `disk` driver, Finder picks up
   the volume.
2. **No app installation required.** macOS ships the driver.
3. **No moments of friction.** No mode toggle on the device, no
   permissions, no race conditions, no alerts.
4. **No collateral damage to other apps.** Image Capture,
   Photos, and other USB-aware apps continue working unaffected.
5. **Local-disk-speed performance.** Random-access reads at
   hundreds of MB/s.
6. **Single source of truth for mount state.** Eject in Finder
   = the drive is unmounted, no app-side disagreement.

## Where we already match (or can match cheaply)

| Gap | Status | Lever |
|---|---|---|
| (6) Finder-as-source-of-truth | Partial — Finder shows a Disconnect button that doesn't talk to Comprador | DiskArbitration listener on the mount path; ~30 lines in `MountManager` |
| (2-ish) No app to think about | Mostly — Login Item makes us auto-start; users don't consciously launch | Polish: drag-to-Applications DMG, signed installer flow |
| (3-partially) No steady-state alerts | Today's fail-fast + welcome-window pre-priming means failure produces one expected notification | Done as of 2026-05-17 commit `1441171b` |

## What's fundamentally off the floor

| Gap | Why | Workaround |
|---|---|---|
| (1) Kernel auto-mount | macOS has no native MTP driver. Apple removed Image Capture's MTP-for-non-Apple-devices support years ago. We *are* the driver. | Accept; minimise the userspace cost. |
| (5) Local-disk-speed reads | MTP has no random-access read primitive. Reading byte 100 GB of a 100 GB video requires downloading the whole file first. | Async prefetch (shipped). Survivable, not fast. Below the protocol-design floor; only a different protocol (USB Mass Storage, ADB-FS, hypothetical "MTP v3") would fix it. |
| (part of 3) Phone-side "File Transfer" mode | The user has to tap something on the phone screen. We can't reach into Android settings. | Detect "phone connected, not in MTP mode" and surface a clear prompt; partial coverage exists in `postFileTransferNotification`. |

## The architectural lever — and its real scope

**FUSE-T migration** is the substrate replacement that's been
on the horizon since the NFS-stall investigation (see
[MISTAKES.md §NFS pivot entry 4](MISTAKES.md)). It's been
described in earlier drafts as the universal lever. **It isn't
quite that.** Worth recording exactly what it closes and
doesn't.

**What FUSE-T migration WOULD close:**

- The NFS-RPC-timeout window for slow reads. macOS's NFS client
  enforces ~20–30 s per RPC; FUSE-T's `read()` callback has no
  equivalent kernel-side patience timer. Multi-minute libmtp
  reads stop being a UX class issue.
- The async-prefetch state machine in `cache.go`. FUSE-T's
  per-callback model lets us surface "still loading" natively
  via partial reads, rather than gymnastic JUKEBOX+retry
  coordination.
- The local NFS-server attack surface. The bridge becomes a
  FUSE daemon (no listening socket on 127.0.0.1; closes
  [CLAUDE.md security invariant #1](../CLAUDE.md)).
- The `dist-swiftc` complexity of managing an NFS mount via
  `/sbin/mount`. FUSE-T mounts are managed through the FUSE-T
  daemon's own protocol.

**What FUSE-T migration WOULD NOT close:**

- **The IOKit `USBDeviceOpenSeize` race.** This race exists at
  the USB-host-controller layer ([MISTAKES.md §NFS pivot
  19b](MISTAKES.md) third iteration). `libusb_claim_interface`
  still needs to break the macOS kernel's USB Imaging Class
  binding to the phone's PTP interface, which still requires
  the seize+reenum. FUSE-T doesn't operate at the USB layer.
- **The `ptpcamerad` kill collateral damage.** Same reason as
  above. While Comprador holds the phone's USB interface via
  libmtp, other apps (Image Capture, Photos) lose access.
- **Multi-device serialization at the libmtp/libusb layer.**
  Two phones still need to be claimed via libmtp; the
  kill-and-seize ordering issues persist.
- **Phone-side mode selection.** Still user-controlled on the
  phone screen.

So FUSE-T closes one big class of problems (NFS-client
timeouts and the complexity built to work around them) but
not the IOKit-USB-race class. The thumb-drive-invisibility
gap shrinks but doesn't close.

## Smaller invisibility wins worth shipping

In rough order of impact per effort:

1. **DiskArbitration listener for external Finder unmount.**
   When the user clicks "Disconnect" in Finder's toolbar, our
   `DeviceSession` should learn about it and update its menu
   state. ~30 lines in `MountManager`. Closes the gap-6
   disagreement.

2. **Hide menu icon when nothing is connected and nothing is
   wrong.** Currently the menu bar shows our icon at all
   times. Match the Bluetooth-menu pattern: visible when
   there's something to show, hidden when idle. ~10 lines in
   `AppDelegate`.

3. **Welcome-window suppression beyond first install.** Today
   the window fires on first launch. Make sure it stays a
   one-time event unless the user opens it explicitly via a
   menu item. Verify no path triggers re-display.

4. **Time-to-mount profiling.** Today there's a ~5 s gap
   between `USB attached` and `Starting bridge…`. Some of
   that is the in-Swift settle wait. Cut where possible.

5. **Detect-and-prompt phone-side mode.** When IOKit reports a
   USB device with class 6 (PTP) but libmtp can't claim, the
   phone is in charging-only or PTP-only mode. Surface a
   clear "tap File Transfer on the phone" prompt. ~50 lines.

6. **Quieter logs in release builds.** The bridge emits a lot
   of `FSStat` lines. Useful for debugging, noise for the
   user looking at Console. Gate them behind a verbosity
   flag.

## The ranking

| Move | Impact | Cost |
|---|---|---|
| FUSE-T migration | Closes NFS-timeout class, simplifies state machines, drops listener attack surface | ~1 week — and see [FUSE-T license deliberation](#fuse-t-license-deliberation) below |
| DiskArbitration listener for external unmount | Closes Finder-disagreement gap | ~30 lines |
| Hide menu icon when idle | Visual quietness | ~10 lines |
| Welcome-window suppression | One less surprise on reinstall | ~5 lines |
| Detect-and-prompt phone-side mode | Closes "user forgot to tap File Transfer" gap | ~50 lines |
| Time-to-mount profiling | Cuts the ~5 s gap between attach and mount | TBD |
| Quieter logs | Polish | ~10 lines |

## What invisibility means at the limit

Perfect thumb-drive equivalence isn't reachable while:

- Android phones don't expose USB Mass Storage.
- macOS doesn't ship a native MTP driver.
- The MTP protocol lacks random-access read.

These are below the floor. Within the floor, the levers above
get us substantially closer. The FUSE-T deliberation is the
inflection point — everything else is days-or-hours of polish.

## FUSE-T license deliberation — conclusion: defer indefinitely

### The license as written

Three tiers, retrieved from FUSE-T's GitHub 2026-05-17:

1. **Free for non-commercial use**, with standard BSD-style
   notice / disclaimer / no-endorsement attribution conditions.
2. **Commercial use *or* bundling with commercial software**
   requires a separately-negotiated commercial license from
   the FUSE-T authors.
3. The **LIBFUSE library** included in FUSE-T's distribution is
   LGPL (forked from osxfuse). Different propagation rules
   than the FUSE-T binary itself.

### Comprador's status under the license

Strictly read, Comprador is free, donation-supported, and
open-source. We don't sell, we don't charge, donations are
gifts not exchanges. We are not "commercial use" by the
standard FOSS-license interpretation.

Two ways we could integrate FUSE-T:

- **Bundle FUSE-T's `.pkg` inside our `.dmg` installer.** Could
  plausibly invoke the "bundling with commercial software"
  clause depending on whether the FUSE-T authors interpret
  Comprador as "commercial software." Most likely safe;
  ambiguous enough to leave us exposed to a future
  commercial-license demand.
- **Require users to install FUSE-T separately.** Shifts the
  license-acceptance burden to the user. Unambiguously
  legal. Substantial UX cost: first-install friction goes
  from "drag .dmg to Applications" to "drag .dmg, install
  Comprador, install FUSE-T, approve FUSE-T's System
  Extension, restart, then Comprador works."

### Why license isn't the load-bearing question

The license problem could be solved (bundle-and-hope or
require-separate-install). The closed-source problem cannot.

FUSE-T sits between the macOS kernel's mount machinery and
our bridge process — exactly the security boundary CLAUDE.md
§Security Invariants is conservative about. Three concrete
risks compound:

1. **No audit.** We can't read FUSE-T's source. We can't
   reason about its security posture from primary materials.
   Comprador would be telling its users "trust us, who trust
   FUSE-T, who we can't verify." That's the exact position
   the security invariants exist to avoid.

2. **No fork on abandonment.** If the FUSE-T authors drop the
   project, get acquired, or change terms, we have no
   recourse. The `.pkg` becomes a fossil. Comprador's mount
   path becomes load-bearing on a dead binary.

3. **No bug-fixing.** If FUSE-T mishandles a corner of the
   FUSE protocol or leaks memory under sustained reads, we
   file an issue and wait. Compare to libmtp: open-source
   under LGPL, we've already patched it in our vendor tree
   (the `0d1418ac` revert, the `[INFO] WRITE` line strip).
   We'd give up that affordance.

### What FUSE-T would actually deliver, re-weighed

The earlier framing of FUSE-T as "the architectural lever"
was too generous. Going through gap-by-gap (see the table in
the §What FUSE-T migration WOULD/WOULD NOT close above):

**Closed by FUSE-T:**
- NFS-RPC-timeout class for slow reads.
- `cache.go` async-prefetch state machine.
- Local-NFS-listener attack surface.

**NOT closed by FUSE-T:**
- IOKit `USBDeviceOpenSeize` race ([MISTAKES 19b](MISTAKES.md)).
- `ptpcamerad` collateral damage.
- Multi-device libmtp coordination.
- Phone-side mode selection.

The kept-open list contains the user-visible UX problems we
spent today's three-step arc resolving. **FUSE-T does not
address them.** The closed list contains problems we already
solved at the application layer:

- NFS-timeout class: solved by `.metadata_never_index`
  sentinel (`56c44372`) + `NFS3ERR_JUKEBOX` threshold
  (`1acdf7f7`) + async prefetch (`a405ed48`).
- Async-prefetch complexity: built, working, well-understood.
- Local-NFS-listener attack surface: real but small (loopback
  only, no remote exposure).

**We built around the FUSE-T-shaped hole, and the workarounds
turned out to be smaller than the migration would be.**

### Decision

**Defer FUSE-T migration indefinitely.** Revisit if any of
the following triggers fire:

- **User feedback names the residual:** sustained reports of
  read-latency problems that the async prefetch doesn't
  cover. (We haven't shipped to enough users to know.)
- **Apple deprecates the NFS client path** in a future macOS.
  Possible but unlikely on the v0.4.0 timescale.
- **FUSE-T becomes open-source under a permissive license**, or
  an open-source FUSE-on-macOS alternative reaches working
  state. (No current credible candidate.)
- **Comprador's user base reaches a scale where the
  application-layer fixes show stress** in ways the FUSE-T
  substrate wouldn't. Currently no evidence; speculative.

### Open question (out-of-band research)

How hard would an in-house FUSE-T-equivalent be? This is
worth knowing for completeness, because if it's months not
years, the long-term audit/maintainability argument flips.
Tracked as research-only; not blocking the deferral decision.
