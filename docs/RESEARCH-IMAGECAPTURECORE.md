# Research: ImageCaptureCore as the ptpcamerad-coexistence path

**Status — 2026-05-10:** preliminary findings from a desk-research
pass. Not a decision; not a plan. The hypothesis is significant
enough to warrant verification with empirical tests against a
physical device before any implementation work begins.

## The question

Comprador today wins the USB-interface claim race against
`ptpcamerad` by killing it (and racing the ~60 ms launchd
respawn). When the bridge loses the race, the user is told to
unplug and replug ([TODO.md](../TODO.md) "App-after-plug failure
is unwinnable from any non-SIP-disabled path"). The Architect
asked: can we connect to the phone without disrupting
ptpcamerad at all?

## What desk research turned up

`ptpcamerad` is a **userspace XPC broker**, not a kernel driver
and not an IOKit-USB matcher. Its
[LaunchAgent plist](https://opensource.apple.com/source/launchd/launchd-842.92.1/launchd/src/launchctl.c)
has `RunAtLoad: false`, no `KeepAlive`, no `LaunchEvents` IOKit
dictionary. It's launched on demand by other processes that want
PTP services — Image Capture, Photos, or other consumers — via
the `com.apple.ptpcamerad` Mach service. **It isn't a USB
matcher.** The actual USB interface claim happens inside a
private framework
(`/System/Library/PrivateFrameworks/ImageCaptureDevices.framework`)
loaded by ptpcamerad when launched.

The public-API path that *normal Apple apps* use to talk to PTP/
MTP devices is **ImageCaptureCore.framework** (SDK headers at
`MacOSX.sdk/.../ImageCaptureCore.framework/Versions/A/Headers`).
This framework brokers access through the XPC service
`mscamerad-xpc.xpc` — the same coordinator ptpcamerad uses. The
relevant public APIs:

| API | Purpose | Available |
|---|---|---|
| `ICDeviceBrowser` | Enumerate PTP/MTP/scanner devices | macOS 10.4+ |
| `ICDevice.requestOpenSession[WithOptions:completion:]` | Open a session on a device (non-exclusive — the framework brokers; multiple holders are supported) | macOS 10.4+ / 10.15+ |
| `ICDevice.requestCloseSession[WithOptions:completion:]` | Close the session | macOS 10.4+ / 10.15+ |
| `ICCameraDevice.requestReadDataFromFile:atOffset:length:` | Read N bytes from a file at offset | macOS 10.4+ |
| `ICCameraDevice.requestDownloadFile:options:` | Download an entire file | macOS 10.4+ |
| `ICCameraDevice.requestSendPTPCommand:outData:completion:` | Send a raw PTP command (escape hatch for anything not exposed directly) | macOS 10.4+ / 10.15+ |
| `ICCameraDevice.requestUploadFile:options:` | Upload a file to the camera | **Deprecated macOS 14+** with: "Sandbox restrictions prohibit writing directly to device hardware" |

The deprecation note on `requestUploadFile` is the central
caveat. Reads are presumably allowed; writes via the
high-level upload API are explicitly sandbox-blocked as of
macOS 14. Whether PTP-level writes via `requestSendPTPCommand`
are *also* blocked, or whether the deprecation is selective, is
the key empirical question.

## Why this matters

If ICDeviceBrowser/ICCameraDevice works for our use case, we get
a lot for free:

- **No USB-interface seizure.** We join the coordinator that
  brokers the device; we don't compete with it. `ptpcamerad`
  doesn't need to be killed; doesn't need to be unloaded; can
  remain alongside us as the broker it is.
- **No more "app-after-plug failure."** The seizure race
  ([TODO.md](../TODO.md) "unwinnable from any non-SIP-disabled
  path") evaporates. Launch order doesn't matter.
- **No DriverKit dext.** The dext on the roadmap exists to win
  the kernel-level claim before ptpcamerad does. If we don't
  need to claim, we don't need the dext. The 4–6 week Apple
  approval cycle and the system-extension entitlement that has
  burned three of our last releases goes away.
- **MAS distribution becomes plausible.** Sandbox prohibits
  sending signals to system processes (so `killall ptpcamerad`
  is a hard MAS blocker today). Joining the broker doesn't
  require signals. With `requestUploadFile` deprecated, writes
  via `requestSendPTPCommand` are the contingent path — needs
  testing.
- **Multi-device gets cleaner.** No global service-state to
  coordinate between bridges; each bridge just opens its own
  session with the coordinator.

If it doesn't work for our use case (writes fully blocked, no
PTP-command escape hatch, or session semantics incompatible with
our NFS-mount model), nothing changes — we keep the current
libmtp + USB-seizure architecture, the dext stays on the
roadmap, and MAS stays out.

## Empirical tests to run

Each requires a physical phone connected and a small Swift test
binary. Worth ~half a day of work each.

### Test 1: coexistence with ptpcamerad

Goal: confirm that `ICDevice.requestOpenSession` succeeds while
ptpcamerad is already holding a session.

```
1. Plug phone in. Open Image Capture (which launches
   ptpcamerad). Verify ptpcamerad is alive: `ps aux | grep
   ptpcamerad`.
2. Run a small Swift test that:
   - Constructs an ICDeviceBrowser
   - Filters to PTP devices, finds the phone
   - Calls requestOpenSessionWithOptions:completion:
   - Logs the success/error in the completion block
3. Expected: success. The framework brokers, both Image Capture
   and our session coexist.
4. Optional confirm: run requestSendPTPCommand to fetch the
   device info (PTP 0x1001 GetDeviceInfo). Verify it returns
   without disrupting Image Capture.
```

### Test 2: read throughput via requestReadDataFromFile

Goal: confirm we can stream a multi-GB file via
`requestReadDataFromFile:atOffset:length:` with adequate
performance for an NFS mount path.

```
1. Phone has a >1 GB test file in /DCIM/
2. Test binary opens a session, navigates to the file, calls
   requestReadDataFromFile in 4 MiB chunks, measures throughput.
3. Expected baseline: 20-30 MB/s on USB 2.0, 60+ MB/s on USB 3.
   (Should match libmtp throughput because the underlying PTP
   transport is identical.)
4. Acceptable: anything > 10 MB/s end-to-end through an NFS
   mount surface.
```

### Test 3: PTP-level write path via requestSendPTPCommand

Goal: determine whether the macOS 14 sandbox blocks
`requestUploadFile` selectively or blocks PTP writes uniformly.

```
1. Test binary opens a session, sends a PTP SendObjectInfo
   command (0x100C) via requestSendPTPCommand to declare a
   small new file.
2. If 0x100C succeeds, send SendObject (0x100D) with the data.
3. Verify the file appears on the phone.
4. Outcome interpretation:
   - Both succeed → writes are fine via PTP-command escape
     hatch; only the high-level upload API is sandboxed.
   - 0x100C is blocked → writes are uniformly blocked at the
     framework layer; ImageCaptureCore is read-only for our
     purposes on macOS 14+.
```

### Test 4: sandbox-app behavior

Goal: confirm tests 1–3 work from a sandboxed app (`app-sandbox`
entitlement: true).

```
1. Re-run tests 1–3 from a Swift binary signed with the same
   entitlement set SwiftMTP uses
   (com.apple.security.app-sandbox + com.apple.security.device.usb +
   com.apple.security.cs.disable-library-validation).
2. Outcome interpretation: if all pass sandboxed, MAS is
   plausible. If test 3 fails sandboxed but passes
   non-sandboxed, MAS would require direct-distribution for
   writes.
```

## Decision tree from the test outcomes

```
Test 1 (coexistence) fails
  → ImageCaptureCore path is dead. Stay with libmtp + seizure.
    Dext stays on roadmap. MAS stays blocked. No change.

Test 1 passes, Test 2 fails (throughput < 10 MB/s)
  → Coexistence works but reads are too slow for NFS mount.
    Worth investigating chunk size, request pipelining, but
    likely a dead end for our use case. Stay with libmtp.

Tests 1, 2 pass, Test 3 fails
  → ImageCaptureCore is viable for reads only. Two-path
    architecture: ICDeviceBrowser for reads (joins broker,
    no seizure, MAS-friendly), libmtp for writes (current
    seizure path). MAS still blocked on writes, but the
    write path can be a Finder Service or out-of-band rather
    than the mount.

All four tests pass
  → ImageCaptureCore is a complete replacement for libmtp.
    Plan a pivot: read + write entirely through the public
    API. Dext is canceled. MAS becomes a roadmap item.
    This is the best outcome and would be a significant
    architecture change.
```

## What I am NOT claiming

- That this will work. The desk research is suggestive, not
  conclusive. The empirical tests are the load-bearing step.
- That if it works, the migration is trivial. ImageCaptureCore
  is delegate-callback-based Objective-C — a meaningful shift
  from libmtp's synchronous-cgo-with-callbacks model. The Go
  bridge would either need a Swift bridge layer in front of it
  or a full rewrite of the MTP layer in Swift.
- That ICDeviceBrowser/ICCameraDevice fully covers what libmtp
  exposes. Some MTP devices have edge-case object types
  (playlists, contacts, calendar entries) that libmtp handles
  but ICCameraDevice may not. For Comprador's primary use case
  (photos, videos, files in `/Internal storage/`,
  `/DCIM/Camera/`, `/Download/`), the coverage should be
  adequate — but verify.

## What this changes about the roadmap (if confirmed)

- **DriverKit dext: probably canceled.** The dext exists to
  win the kernel-claim race. If we don't need to claim, we
  don't need the dext. Cancellation saves a 4–6 week Apple
  approval cycle and the recurring entitlement-related release
  pain.
- **`killall ptpcamerad`: removed.** Eliminates the failure
  mode where Comprador's crash leaves Image Capture broken
  until reboot (which `launchctl unload` *would* have caused,
  per [letter 08](../correspondence/08-on-ptpcamerad-cleaniness/letter.md);
  the kill avoids it because launchd respawns).
- **MAS distribution: re-enters the planning conversation.**
  Currently a non-goal per [CLAUDE.md](../CLAUDE.md). If the
  empirical tests show MAS-compatibility, the cost-benefit
  shifts; worth re-deliberating.
- **`USBSeizer.swift` and the `--seize-for-vendor` /
  `--seize-for-product` flags on the bridge: removed.**
- **The privileged helper: removable on a faster schedule.**
  Already slated for v0.4.0 removal; if the seizure path also
  goes, there's nothing left in the helper that we still need.
- **Per-device bridge targeting (PLAN-MULTI-DEVICE.md §6):
  changes from "filter libmtp's raw-device list by Location
  ID" to "use ICDeviceBrowser's deviceFromUSBLocationID or
  equivalent."** Simpler.

## Receipts

- `ptpcamerad` plist: `/System/Library/LaunchAgents/com.apple.ptpcamerad.plist`
- `mscamerad-xpc.xpc`:
  `/System/Library/Frameworks/ImageCaptureCore.framework/Versions/A/XPCServices/mscamerad-xpc.xpc`
- `ImageCaptureDevices.framework` (private, linked by ptpcamerad):
  `/System/Library/PrivateFrameworks/ImageCaptureDevices.framework`
- SDK headers:
  `/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/System/Library/Frameworks/ImageCaptureCore.framework/Versions/A/Headers/`
- The deprecation message on `requestUploadFile`:
  `ICCameraDevice.h:318` —
  `IC_DEPRECATED("Sandbox restrictions prohibit writing directly to device hardware", macos(10.4,14.0))`

## How this doc should evolve

This is a research note, not a plan. When the empirical tests
run, this doc should grow a "Test results" section with the
actual numbers. If test 1 fails, the doc becomes a closed
investigation. If tests pass, the doc spawns
`docs/PLAN-IMAGECAPTURECORE-PIVOT.md` and this becomes the
historical record of how we got there.
