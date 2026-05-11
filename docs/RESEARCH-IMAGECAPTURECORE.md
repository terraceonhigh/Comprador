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

Each test below is structured as a small experiment with its own
hypothesis, design, and (after the test runs) results and
conclusion sections. The structure is deliberate: write the
hypothesis *before* the test so the conclusion is not retrofitted
to whatever happens. Results and Conclusion sections are
placeholders to be filled in after running each test.

All four tests require a physical phone connected via USB. Each
needs a small Swift binary; the binaries can share a common
`ICDeviceBrowserDelegate` skeleton so the marginal cost per test
after the first is small.

---

### Test 1: coexistence with ptpcamerad

**Hypothesis.** `ICDevice.requestOpenSessionWithOptions:completion:`
will succeed even when `ptpcamerad` is already holding a session
on behalf of another client (Image Capture). The reasoning:
ptpcamerad is a userspace XPC broker, not a USB-interface
exclusive-holder. The `mscamerad-xpc.xpc` coordinator mediates
between N clients. Image Capture and Photos already coexist on a
single device today via the same broker; our session should too.

**Experiment design.**

*Setup.*
- macOS 26.4 (or whatever the current dev machine runs).
- Phone: Sony Xperia 10 III (the project's primary test device,
  per [letter 01](../correspondence/01-on-the-finder-handshake/letter.md)),
  in File Transfer mode, plugged into a USB-C port directly (no
  hub, to eliminate enumeration variance).
- `ptpcamerad` running. Confirm with `pgrep -lf ptpcamerad`. If
  not running, open Image Capture.app once to trigger launch; it
  stays alive for several minutes after Image Capture is closed.
- A test binary at `bridge/cmd/ictest1/main.swift` (or similar
  path) compiled with `swiftc` against `ImageCaptureCore.framework`.

*Procedure.*

1. Test binary instantiates an `ICDeviceBrowser`, sets `self`
   as delegate, and calls `start`.
2. In `deviceBrowser:didAddDevice:moreComing:`, log the device's
   `name`, `usbVendorID`, `usbProductID`, `transportType`,
   `UUIDString`.
3. Filter for the phone by matching `usbVendorID == 0x0FCE`
   (Sony) and `usbProductID` in the Sony range (or by name
   match `"XQ-BT52"`).
4. Set the test binary as the device's `delegate`, conforming
   to `ICDeviceDelegate`.
5. Call `requestOpenSessionWithOptions:nil completion:` and
   capture the timestamp before and after the completion block
   fires.
6. In the completion block, log: `error == nil`, the error's
   `code` and `domain` if present, elapsed time from request to
   completion.
7. If session opens, call `requestCloseSessionWithOptions:nil
   completion:` and log the close result.

*Observations to collect.*
- Whether `requestOpenSession` succeeds (binary).
- The error code/domain if it fails.
- Elapsed time from request → completion (informs Test 2's
  latency expectations).
- Whether Image Capture continues to function during and after
  our session is open (manual check: drag a thumbnail in Image
  Capture while our session is alive).
- `ptpcamerad` process state before, during, after (does it
  stay alive? get killed? respawn?).

*Pass criteria.* `requestOpenSession` returns `error == nil`
*and* Image Capture continues to function normally while our
session is held.

*Failure modes to watch for.*
- `error.domain == ICErrorDomain, error.code == ICDeviceErrorXXXX`
  → framework refuses concurrent sessions; coexistence
  hypothesis is falsified.
- `requestOpenSession` completion never fires (>30 s timeout)
  → framework is hung or our delegate isn't wired correctly.
  Retry with `IC_AVAILABLE` checks; verify delegate is retained.
- Image Capture's UI freezes / disconnects → broker refuses to
  serve two clients; falsifies a weaker version of the
  hypothesis.

**Results.** *(pending)*

**Conclusion.** *(pending)*

---

### Test 2: read throughput via requestReadDataFromFile

**Hypothesis.** `ICCameraDevice.requestReadDataFromFile:atOffset:length:`
will sustain at least 10 MB/s for sequential reads on a USB 2.0
connection (the project's worst-case transport) and at least
30 MB/s on USB 3. The reasoning: the underlying PTP transport is
identical to what libmtp uses, so throughput should be in the
same order of magnitude as the libmtp baseline we already
observe. The 10 MB/s floor is the minimum where dragging a
1 GB file through an NFS mount feels acceptable rather than
broken (~100 s for 1 GB).

**Experiment design.**

*Setup.*
- Phone with a known test file in `/Internal storage/DCIM/`
  (recommended: `Shrek.2001.1080p.BluRay.x264.YIFY.mp4` from
  the project's `testfiles/`, sideloaded via Test 3's path or
  pre-staged via Finder while libmtp still works). File size
  must be > 256 MB to amortize per-request overhead.
- Open session from Test 1 already established.
- USB 3 connection where possible; note USB version in
  `system_profiler SPUSBDataType | grep -A5 Sony` output so the
  result is interpretable.

*Procedure.*

1. After session open, request the device's content tree
   (`device.contents`) — wait for delegate callback
   `cameraDevice:didReceiveContent:`.
2. Navigate the tree to find the test file (matching by name).
3. Record `start = mach_absolute_time()`.
4. Loop calling `requestReadDataFromFile:` in 4 MiB chunks
   (offset = 0, 4 MiB, 8 MiB, ...) until end-of-file. Each chunk
   is delivered to the delegate's
   `cameraDevice:didReceiveData:fromFile:` selector; chain into
   the next request from that callback.
5. Sum total bytes; record `end = mach_absolute_time()`.
6. Throughput = total_bytes / elapsed_seconds, in MB/s.
7. Compute MD5 of received bytes; compare to phone-side MD5 (via
   Test 3 if writes work, or via libmtp pre-staged hash).

*Observations to collect.*
- Total elapsed time and bytes.
- Throughput in MB/s.
- Per-chunk latency (max, p99, p50): if any chunk takes >1 s,
  the NFS mount will surface as laggy regardless of average
  throughput.
- USB transport version (2.0 vs 3.x).
- Memory footprint of the test binary during the transfer (via
  `vmmap` or just `ps -o rss=`). The cgo-callback leak that
  affects the libmtp path
  ([MISTAKES.md 8a](MISTAKES.md)) should *not* exist here —
  ImageCaptureCore manages its own buffers — but verify.
- MD5 round-trip vs. source.

*Pass criteria.* Throughput ≥ 10 MB/s sustained over the entire
file *and* MD5 round-trip matches *and* no per-chunk latency
exceeds 2 s.

*Failure modes to watch for.*
- Throughput < 10 MB/s → coexistence works but reads are too
  slow for NFS-mount-grade UX. Investigate chunk size (try 16
  MiB), request pipelining (issue read N+1 before read N
  completes).
- MD5 mismatch → framework-level corruption. Bug or feature
  flag we're missing.
- Per-chunk latency spikes → request queueing inside the
  framework; the XPC broker may be round-robining between
  concurrent clients. Test in isolation (close Image Capture)
  to see if throughput improves.
- Memory growth proportional to file size → framework has its
  own version of the cgo-callback leak; would need to be
  reported upstream and could disqualify this path for large
  files.

**Results.** *(pending)*

**Conclusion.** *(pending)*

---

### Test 3: PTP-level write path via requestSendPTPCommand

**Hypothesis.** `ICCameraDevice.requestSendPTPCommand:` *will*
allow PTP-level write commands (specifically `SendObjectInfo`
0x100C followed by `SendObject` 0x100D) to succeed on macOS 14+
despite `requestUploadFile`'s deprecation. The reasoning: the
deprecation text refers to "writing directly to device hardware,"
which more plausibly describes the abstraction `requestUploadFile`
exposes than it does the raw PTP transport that
`requestSendPTPCommand` is named for. Apple deprecated the
high-level API for product reasons (sandbox-friendly behavior in
MAS apps) but it would be inconsistent to leave a public escape
hatch named "send a PTP command" that doesn't actually send PTP
commands. The hypothesis is contingent and the test is the
load-bearing one for the whole ImageCaptureCore pivot — if writes
fail uniformly, the path is read-only.

**Experiment design.**

*Setup.*
- Open session from Test 1.
- A small local file to upload — recommend 1 MiB of `/dev/urandom`
  saved as `comprador-write-test.bin`. Large enough to exercise
  the data phase, small enough to be quick.
- Verify phone-side write target: `/Internal storage/Download/`
  (writable on Android phones in MTP mode).

*Procedure.*

1. Construct the `SendObjectInfo` (0x100C) PTP command
   container per PTP 1.0 spec § 5.4.3:
   - OpCode: 0x100C
   - Parameter 1: storage ID (from previous `GetStorageIDs`
     0x1004 call)
   - Parameter 2: parent object handle (from `GetObjectHandles`
     0x1007 call against `/Download`)
   - Data phase: `ObjectInfo` dataset (StorageID, ObjectFormat
     0x3000 = Undefined Type, ObjectCompressedSize = file size,
     filename, etc.)
2. Call `requestSendPTPCommand:outData:completion:` with
   the command bytes and the ObjectInfo data.
3. In the completion, parse `responseData` for the new object
   handle (Parameter 1 of the response).
4. Construct `SendObject` (0x100D) PTP command:
   - OpCode: 0x100D
   - No parameters (the handle is implicit from prior
     SendObjectInfo)
   - Data phase: the actual file bytes
5. Call `requestSendPTPCommand:` with command + 1 MiB data.
6. Verify the file appears on the phone (manually browse the
   phone's Downloads in Files app, OR re-read via Test 2's
   `requestReadDataFromFile` and compare MD5).

*Observations to collect.*
- Whether `SendObjectInfo` completion fires with `error == nil`
  and a valid response code (PTP response 0x2001 = OK).
- Whether `SendObject` completion fires with `error == nil` and
  PTP response 0x2001.
- The file's presence and integrity on the phone after.
- If failure: the exact error code/domain.

*Pass criteria.* Both PTP commands return OK *and* the file is
present on the phone with matching MD5.

*Failure modes to watch for.*
- `SendObjectInfo` errors with permission/sandbox shape →
  framework blocks writes uniformly; ImageCaptureCore is
  read-only for our purposes.
- `SendObjectInfo` succeeds, `SendObject` fails → partial
  state; investigate whether the half-created object can be
  cleaned up via `DeleteObject` (0x100B).
- Both succeed, file missing → unlikely but indicates a
  framework-level filter that drops the data phase.
- Both succeed, file present, MD5 mismatch → data-phase
  corruption; could indicate framework chunking interferes.

**Results.** *(pending)*

**Conclusion.** *(pending)*

---

### Test 4: sandbox-app behavior

**Hypothesis.** Tests 1–3 will all pass when re-run from a Swift
binary signed with the App Sandbox entitlement
(`com.apple.security.app-sandbox = true`) plus
`com.apple.security.device.usb = true`. The reasoning: SwiftMTP
ships with exactly this entitlement set
([SwiftMTP.entitlements](../../SwiftMTP/SwiftMTP/SwiftMTP.entitlements))
and successfully drives PTP/MTP operations through the same Mac
that hosts ptpcamerad. The ImageCaptureCore framework is
expressly designed for sandboxed clients (Image Capture itself
is sandboxed). If anything, Test 3 is the weak link: the
sandbox-deprecation-message on `requestUploadFile` suggests
Apple cares specifically about sandboxed writes, so the
PTP-command escape hatch may be selectively blocked under
sandbox even if it works without.

**Experiment design.**

*Setup.*
- Wrap the test binaries from 1–3 into a single Swift app bundle
  signed with:
  - `com.apple.security.app-sandbox` = true
  - `com.apple.security.device.usb` = true
  - `com.apple.security.cs.disable-library-validation` = true
    (only if needed for bundled dylibs)
  - `com.apple.security.files.user-selected.read-write` = true
    (so the bundle can read the test write-file from disk)
- Sign with the Developer ID Application cert and our
  signing identity.
- Same phone, same session, same procedure as Tests 1–3.

*Procedure.* Run Tests 1, 2, and 3 in sequence within the
sandboxed app. Collect the same observations as in each unsandboxed
counterpart. Compare to the unsandboxed baseline.

*Observations to collect.*
- For each of 1, 2, 3: does the test pass identically?
- If any test fails under sandbox but passed unsandboxed: the
  precise error code/domain.
- Whether the entitlement set above is sufficient or whether
  additional entitlements surface as TCC prompts (e.g., USB
  device access permission dialog on first run).

*Pass criteria.* All three sub-tests pass with the same results
as their unsandboxed counterparts, with no additional user-
visible permission prompts beyond the first-launch USB-device
TCC dialog (if any).

*Failure modes to watch for.*
- Test 1 fails sandboxed but passed unsandboxed → framework
  refuses sandboxed clients; MAS distribution is dead via
  this path.
- Test 2 fails sandboxed (sustained throughput drops) → sandbox
  IPC overhead is killing performance. Investigate; may be
  fixable with bigger chunk sizes.
- Test 3 fails sandboxed but passed unsandboxed → MAS-readable,
  not MAS-writable. Hybrid architecture (read via API,
  write via the helper or out-of-band).
- An unexpected TCC prompt appears → identify which entitlement
  it gates on; add to the test bundle and re-run.

**Results.** *(pending)*

**Conclusion.** *(pending)*

---

### Filling in Results and Conclusion

After running each test, replace the `*(pending)*` placeholders
with the following structure (in the same doc, in place):

```
**Results.** Concrete observations from running the experiment.
Numbers (throughput, latency, memory), error codes if any, the
phone model and macOS version under test, the date. Keep it
factual — interpretation belongs in Conclusion.

**Conclusion.** What the results mean for the hypothesis (was it
confirmed, falsified, or partially supported?). What this changes
about the decision tree below. Any unexpected findings that
should spawn follow-up tests.
```

Each test should be filled in independently as it runs — don't
wait for all four to complete before recording 1's results.

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
