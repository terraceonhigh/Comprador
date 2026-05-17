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

## FUSE-T license deliberation

*Below this point is the deferred FUSE-T license thinking, to
be filled in when the architect and I work through the
non-commercial/commercial line and the closed-source
maintenance-risk question. The license text is reproduced in
the conversation transcript; key facts to decide against:*

- *FUSE-T binary is closed-source (only `.pkg` is published on
  GitHub).*
- *Free for non-commercial use with attribution conditions.*
- *Commercial use OR bundling with commercial software
  requires a commercial license.*
- *Comprador's status under this license: ambiguous. We're
  free, donation-supported, and would either bundle FUSE-T's
  `.pkg` in our installer or require users to install it
  separately. The first invokes the "bundling" clause; the
  second shifts the license-accept burden to the user.*

To be drafted in a follow-up commit.
