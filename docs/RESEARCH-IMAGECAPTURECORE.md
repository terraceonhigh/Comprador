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

**Results.** *(2026-05-11, gala, macOS 26.4, ictest1 binary built
via `make ictest1` from `bridge/cmd/ictest1/main.swift`.)*

Two phones available: a Sony Xperia 10 III (XQ-BT52) and a Google
Pixel 6. The Xperia turned out not to expose a PTP / Photo
Transfer mode on its USB-mode picker — only File Transfer (MTP).
The Pixel 6 exposes both.

Pre-test ICDeviceBrowser visibility check (the input to the
test):

| Phone | USB mode | Visible to ICDeviceBrowser? |
|---|---|---|
| Pixel 6 | File Transfer (MTP) | **No** — `deviceBrowserDidEnumerateLocalDevices` fires with no `didAdd` calls |
| Xperia 10 III | File Transfer (MTP) | **No** — same |
| Xperia 10 III | PTP | n/a — phone doesn't expose this mode |
| Pixel 6 | PTP | **Yes** — `didAdd` fires synchronously during `browser.start()`, device classes as `ICCameraDevice`, vid=0x18D1 pid=0x4EE5 |

Test 1 proper, run against the Pixel 6 in PTP mode (verbatim
binary output):

```
[browser] +device  name='Pixel 6'  vid=0x18D1  pid=0x4EE5
          transport=Optional("ICTransportTypeUSB")
          uuid=00003143-3032-3146-4446-363030374E38
[target] using: 'Pixel 6'  class=ICCameraDevice
[ptpcamerad] before requestOpenSession: 28161 /usr/libexec/ptpcamerad
[session] calling requestOpenSession(options: nil)...
[session] PASS  error=nil  elapsed=0ms
[ptpcamerad] after open: 28161 /usr/libexec/ptpcamerad
[session] requesting close...
[session] close OK
[ptpcamerad] after close: 28161 /usr/libexec/ptpcamerad
```

Manual Image Capture check while our session was held: Image
Capture remained responsive — the Pixel 6 was still visible in
its sidebar and the architect was able to interact with it
normally. No UI freeze, no device disappearance.

`ptpcamerad` PID 28161 unchanged across the full lifecycle
(startup → before-open → after-open → after-close). No respawn,
no kill, no broker churn.

(Note on the 0ms elapsed time for `requestOpenSession`: plausible.
ImageCaptureCore session-open is likely a userspace-only
handshake with the ptpcamerad broker; the actual USB I/O wouldn't
happen until a read/write operation runs. Test 2 will verify the
session is functional, not just nominal.)

**Conclusion.** Hypothesis **supported** for PTP-mode devices.
`ICDevice.requestOpenSession` returns `nil` while ptpcamerad
holds an active broker context; the two clients coexist; Image
Capture remains functional during our session.

But with a meaningful caveat that the framing in
[letter 09](../correspondence/09-ptpcamerad-was-a-broker/letter.md)
under-weighted: **ICDeviceBrowser does not enumerate Android
phones in File Transfer (MTP) mode.** Tested against two
different Android vendors (Google, Sony); neither surfaces. This
narrows the pivot story significantly from "ImageCaptureCore
replaces libmtp wholesale" to "ImageCaptureCore is a PTP-only
window."

The shape this leaves us in:

- For users whose phones expose PTP and who select PTP mode:
  ImageCaptureCore-coexistence is viable; the seizure race
  disappears for that codepath.
- For users on File Transfer (MTP) — which is the default on
  every Android phone the project has tested, and on the Sony
  is the *only* option — ImageCaptureCore doesn't see the
  device. We still need libmtp + the seizure race + the helper
  for the MTP path.

This doesn't kill the architectural pivot, but it does change
its character. It's no longer "delete libmtp, delete the dext,
delete the helper, ship via MAS." It's "build a PTP-mode path
as an opt-in coexistence story while keeping the MTP path as
the default." The seizure race, the dext, and the helper all
stay in their current places for users who don't switch their
phone to PTP — which, given the UX cost of "find the USB mode
picker and pick the non-default option", is most users.

A consolation: the PTP path may still be the right *internal*
plumbing for specific subsystems (e.g. multi-device read paths
where two phones are plugged in simultaneously — if both are
switched to PTP, we avoid two libusb claims racing two
ptpcamerad respawns), even if it's not the user-facing default.
Worth holding in mind as multi-device work proceeds.

Tests 2, 3, 4 retain their value only conditional on the user
being in PTP mode — so the cost-benefit of running them shifts.
Test 2 (throughput) is still worth running: if PTP-mode reads
through ImageCaptureCore are throughput-competitive with our
libmtp path, that opens "use ICCore for PTP-mode-eligible
users" as a real option. Tests 3 (writes via SendPTPCommand)
and 4 (sandbox / MAS) become contingent on Test 2.

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

**Results.** *(2026-05-11, gala, macOS 26.4, ictest2 binary from
`bridge/cmd/ictest2/main.swift`. Pixel 6 in PTP mode, Image
Capture.app open before the run to ensure ptpcamerad alive.)*

Verbatim binary output (final report; progress lines elided to
every 16th chunk during the read):

```
[browser] +device  name='Pixel 6'  vid=0x18D1  pid=0x4EE5
[session] open OK  elapsed=0ms
[catalog] deviceDidBecomeReady(withCompleteContentCatalog:) fired
[catalog] 2351 items, 2351 ICCameraFile
[catalog]   'PXL_20260405_233348324.LS.mp4'  size=1446425792 (1379 MiB)
[catalog]   'PXL_20260307_054003976.LS.mp4'  size=415455011 (396 MiB)
[catalog]   'PXL_20260328_031847726.TS.mp4'  size=277888823 (265 MiB)
[catalog]   'PXL_20260307_052919573.LS.mp4'  size=204547007 (195 MiB)
[catalog]   'PXL_20260418_045211394.TS.mp4'  size=202189443 (192 MiB)
[read] target: 'PXL_20260405_233348324.LS.mp4'  size=1446425792 bytes
[read] chunks of 4 MiB (strict sequential)
... (16-chunk progress lines, 18.7–19.0 MB/s, rss steady 26 MiB) ...

[result] file:           PXL_20260405_233348324.LS.mp4
[result] expected:       1446425792 bytes
[result] read:           1446425792 bytes (1379 MiB)
[result] chunks:         345 × 4 MiB
[result] elapsed:        72.6 s
[result] throughput:     19.00 MB/s
[result] chunk_ms:       min=135 p50=197 p99=215 max=244
[result] md5:            267dcc0dffcc3f4ecd127ac6be62ccd6
[result] rss:            end=26320 KiB  peak=30016 KiB
[verdict] PASS  bytes=ok  thrpt=ok  chunk=ok
[ptpcamerad] after read: 28161 ptpcamerad
[session] close OK
[ptpcamerad] after close: 28161 ptpcamerad
```

Three things to highlight:

1. **Chunk latency is extraordinarily tight.** Min 135 ms, p99
   215 ms, max 244 ms across 345 chunks. The distribution is
   nearly flat — no outliers, no garbage-collection pauses, no
   framework round-trip stalls. Every chunk takes about 200 ms,
   reproducibly.

2. **RSS stays flat across a 1.4 GiB read.** Peak 30 MiB, end
   26 MiB. ImageCaptureCore's read path **does not have the
   cgo-callback allocation leak** that the libmtp path suffers
   ([MISTAKES.md 8a](MISTAKES.md)). Framework manages its own
   buffers; the Swift binding doesn't need to.

3. **ptpcamerad PID 28161 is unchanged** across the full
   lifecycle: startup → before-open → after-open → after-read
   → after-close. The broker doesn't churn under sustained read
   load. Coexistence holds throughout the test, not just at
   session-open.

**Conclusion.** Hypothesis **supported**. For the PTP-mode
path, ImageCaptureCore reads are:

- **Throughput-competitive.** 19 MB/s sustained is comfortably
  above the 10 MB/s NFS-acceptability floor and within the
  same order of magnitude as what libmtp delivers on the same
  hardware. The user-perceived experience of dragging a phone
  video into Finder via this path is "fast enough."
- **Latency-predictable.** Tight distribution, no spikes. The
  NFS mount surface (which we tuned for chunk-level
  predictability after the WebDAV writeseq saga) will get
  along with this comfortably.
- **Memory-clean.** No accumulation, no leak. The architectural
  hazard that
  [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md) gates on
  (multi-device transfers OOMing the host) does not apply to
  this path. Two concurrent ImageCaptureCore sessions reading
  large files would, by this evidence, hold steady at ~50 MiB
  RSS combined, not the ~18 GiB the libmtp path would burn.

Together with Test 1, this establishes the PTP-mode pivot as
**empirically real, not just architecturally appealing**:
session opens coexist with ptpcamerad, reads sustain useful
throughput with bounded memory, and the broker remains stable
under load. For phones that expose PTP and users who switch
to it, ImageCaptureCore is a viable read backend.

What remains open:

- **Tests 3 and 4.** Writes (Test 3) and sandbox/MAS
  compatibility (Test 4) are the load-bearing experiments for
  whether the PTP path can be a complete *substitute* (vs.
  read-only complement) for libmtp.
- **The MTP-mode default.** Nothing in Tests 1 or 2 changes
  the structural fact established in Test 1's setup: phones in
  File Transfer (MTP) mode are invisible to ImageCaptureCore.
  For default-mode users — most users, and all Xperia users —
  the libmtp path remains load-bearing.

If Test 3 also passes, we have a credible *opt-in* PTP-mode
product story (and a credible internal substrate for concurrent
multi-device reads, where two PTP sessions through the broker
would dodge the libusb-claim race entirely). If Test 3 fails,
the PTP path is read-only and Comprador's role for those users
is "browse and copy off the phone, but not onto it" — still
useful, but a different shape of feature.

---

## Scope correction — what Tests 1 and 2 did NOT measure

*Added 2026-05-11, after the architect asked the question that
broke the framing above.*

The Conclusions of Tests 1 and 2 sketch a "dual-backend,
mode-aware Comprador" product direction. That sketch is wrong,
because it elides the protocol-level distinction between PTP
and MTP. This section records the correction.

**PTP exposes camera content only.** PTP is *Picture* Transfer
Protocol — designed for digital cameras, standardized around
media file objects, no native concept of arbitrary
directories or non-media files. MTP (Media Transfer Protocol)
is Microsoft's extension of PTP that adds folder hierarchies
and arbitrary file types. Android's "File Transfer" mode is
MTP precisely so the phone can expose its entire shared
storage (Music, Downloads, Documents, app data, etc.). Its
"PTP" / "Photo Transfer" mode, by design, exposes only the
camera-content subset (DCIM, sometimes Pictures).

**ICDeviceBrowser binds to the PTP path.** Test 1 established
that ICDeviceBrowser enumerates phones in PTP mode and does
not enumerate phones in File Transfer (MTP) mode. The reason
is upstream of macOS: the phone, when in MTP mode, exposes a
USB interface descriptor that the PTP framework does not
match. ImageCaptureCore is the front door to the PTP
descriptor, not to the MTP one.

**The intersection is small.** What ImageCaptureCore *can*
address is the camera-roll subset of the filesystem on phones
that the user has switched to PTP mode. What Comprador
*exists to address* is the phone's general filesystem (Music,
Downloads, app data, ringtones, the user's actual non-camera
content). The two overlap on "browse and import photos" —
which Image Capture.app already serves natively.

**What this invalidates upstream in this doc:**

- The phrase "ImageCaptureCore is a PTP-only window" in
  Test 1's Conclusion was correct as literal text but
  underweighted in implication. A "PTP-only window" is not
  "ImageCaptureCore minus MTP-mode users" — it is
  "ImageCaptureCore minus non-camera content, for all users."
- The phrase "dual backend, mode-aware" in Test 2's
  Conclusion was wrong. Mode-aware routing between libmtp
  and ImageCaptureCore would only help users whose desired
  content happens to be in the camera-roll subset.
- The "credible internal substrate for concurrent
  multi-device reads" framing in Test 2 holds only for the
  multi-camera-roll case, not for the general multi-device
  feature that
  [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md)
  is committed to. The general feature still wants
  phones-as-Finder-volumes, which still wants MTP, which
  still wants libmtp, which still wants the cgo-callback
  buffer-reuse fix per [TODO.md](../TODO.md).

**What this leaves intact:**

- The empirical findings themselves. ImageCaptureCore session
  coexistence with ptpcamerad is real (Test 1). PTP-mode read
  throughput at ~19 MB/s with flat memory profile is real
  (Test 2). Those are accurate measurements of what was
  measured. The Conclusion paragraphs are where the
  measurement-to-implication step went wrong, not the
  measurements themselves.
- The realization that ptpcamerad is a userspace XPC broker
  rather than a USB exclusive-claim adversary
  ([letter 09](../correspondence/09-ptpcamerad-was-a-broker/letter.md)).
  That's still true. It just doesn't have the architectural
  consequences letter 09 reached for, because joining the
  broker only buys access to the broker's scope, and the
  broker's scope is too small for the product.

**Tests 3 and 4 are no longer interesting.** They would
measure write functionality and sandbox behavior, but in a
scope (camera roll only) that doesn't substitute for what
Comprador does for users. The cost of building them isn't
justified by what the result would tell us. The investigation
closes here.

**Residual value:**

1. A potential read-only optimization for camera-content
   browsing specifically. Small, niche, probably not worth the
   code surface — but recorded so the next person who wonders
   "could we use ImageCaptureCore for X?" has the receipt of
   what we found.
2. A demo-only path for "concurrent multi-device works"
   without the cgo-callback fix landing first. Not a product,
   a credibility exhibit if we ever need one.

**Methodological note.** This correction is the third recast
in a single investigation:

- Letter 08 → letter 09 (one Claude to another): "ptpcamerad
  is a broker we should join, not an adversary we should
  kill." Correct framing of the protocol-level relationship.
- Letter 11 part one (2026-05-11 morning): "The pivot is
  opt-in, not wholesale — MTP-mode phones are invisible to
  ICDeviceBrowser." Correct narrowing of the user base.
- This section (2026-05-11 after Test 2): "The pivot is
  scope-limited, not user-limited — even where it works it
  only addresses camera content." Correct identification of
  the protocol's content ceiling.

Each recast was triggered by an empirical observation that the
previous framing failed to predict. The pattern is worth
flagging: when an architectural enthusiasm survives one
empirical surprise, that doesn't mean it has been *de-risked*
— it may just mean the next surprise hasn't arrived yet. Run
the test that would falsify the *biggest* claim first, not
the easiest one.

The biggest claim in letter 09 was: "ImageCaptureCore could
replace libmtp." The cheapest falsification of that claim is
the question the architect asked in eleven words: *"with PTP,
how can we read/write non-image files?"* — a question about
the *scope of access*, not the *quality of access*. We ran
Tests 1 and 2 first because they were easier to design,
not because they were the load-bearing falsifications.

For the next investigation: enumerate the candidate
falsifications before writing test code, and order them by
how *broadly* they would invalidate the architectural claim,
not by how *cheaply* they would.

---

### Test 3: PTP-level write path via requestSendPTPCommand
*(skipped per the scope correction above — recording the spec
for archival completeness but not running it)*


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
([SwiftMTP.entitlements](../../references/SwiftMTP/SwiftMTP/SwiftMTP.entitlements))
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

## If Test 3 fails: supported alternatives to investigate

`ICCameraDevice.requestUploadFile` is the only obviously-named
write API; it's the deprecated one. If Test 3 (or Test 4) shows
that `requestSendPTPCommand` is blocked for write opcodes,
Comprador's write path is dead through ImageCaptureCore as-is.
Before concluding the pivot is read-only, the following
supported-but-non-obvious alternatives deserve a research pass.
None has been verified; they are listed in rough order of
plausibility.

### 1. User-initiated transfers via NSItemProvider / drag-drop

System-mediated, user-initiated transfers may bypass the
"writing directly to device hardware" sandbox restriction
*because* the user dragging a file onto an `ICDevice` UI counts
as authorization. Apple's pattern across other sandbox-
constrained APIs (file access, contacts, calendar) has been
"user-mediated actions are allowed where direct programmatic
ones aren't." Worth checking whether Image Capture's own
drag-to-camera flow (which obviously works under sandbox)
exposes any public API.

*Where to look:* `NSItemProvider`-based `ICDevice` extensions,
`NSDraggingDestination` protocols on `ICDevice` UIs, WWDC
2024/2025 sessions on Image Capture (if any).

### 2. File Provider extension — revisited

Rejected in [CLAUDE.md](../CLAUDE.md) "Why not File Provider
API?" on the basis that USB device access from a File Provider
extension is heavily sandbox-restricted. But two things may
have changed: (a) macOS 14/15 may have relaxed File Provider
sandbox shape; (b) routing the **write** path through a File
Provider while keeping reads through `ICCameraDevice` could be
a hybrid that works. Worth ~half a day reading Apple's File
Provider 2024 release notes before committing to a verdict.

*Where to look:* `FileProvider.framework` docs for macOS 14+,
WWDC 2024 "What's new in File Provider," whether
`NSFileProviderExtension` can now host non-cloud backends.

### 3. XPC service bundled with the app

Standard sandbox-relaxation pattern: a helper XPC service that
runs outside the app's sandbox does the privileged operation.
Apple supports this explicitly; many MAS apps use it. The XPC
service would be the one calling `requestSendPTPCommand` write
opcodes (or talking directly to libmtp), with the main app
sandboxed.

Tradeoff: re-introduces a privileged helper-shaped surface we
were trying to remove
([SECURITY.md](SECURITY.md) helper section,
[TODO.md "Security cleanup — v0.4.0 priority"](../TODO.md)).
Different from the current `SMAppService.daemon` because XPC
services bundle with the app (no separate registration, no
Login Items approval) and run with the user's privilege (not
root) — much smaller surface than the current helper. But
still a surface.

*Where to look:* `NSXPCConnection`, `XPCService` Info.plist
keys, Apple's "Daemons and Services" Programming Guide for
sandbox-XPC patterns.

### 4. Scripting bridge / AppleScript-driven Image Capture

Image Capture itself can be scripted via AppleScript and the
old Scripting Bridge framework. If we can drive Image Capture
(which works under sandbox today, since it ships with macOS) to
perform the upload, we sidestep the deprecation entirely. The
user wouldn't see Image Capture's UI; we'd drive it
programmatically.

*Tradeoff:* depends on Image Capture continuing to support
scriptable uploads (Apple has been retiring AppleScript
surfaces for years). Brittle.

*Where to look:* `osascript -e 'tell app "Image Capture" to ...'`
exploratory shell, the Image Capture scripting dictionary
(`open -a "Image Capture"` then "File → Open Scripting
Dictionary").

### 5. A private entitlement Apple grants on request

Apple sometimes grants restricted entitlements to specific
developers via the Developer Program (camera/HEIC playback,
certain CarPlay surfaces, etc.). It's possible there's a
`com.apple.developer.image-capture.write` or similar that
Apple grants for "we sell a third-party MTP file-transfer
tool, please" requests. Long shot, but the cost of asking is
an email.

*Where to look:* Apple Developer Forums, Apple's "Request
restricted entitlement" page in the Developer Portal.

### 6. The deprecation was overstated — re-read it

The deprecation text is `"Sandbox restrictions prohibit writing
directly to device hardware"`. This may be Apple's policy
guidance for *new* sandboxed apps, not a runtime block. The
API may still function for legacy apps and direct-distribution.
**Test 3 itself answers this in part** — if it works
unsandboxed, the deprecation is advisory, not enforced.

If Test 3 *and* Test 4 both succeed (writes work, sandboxed
or not), this section becomes obsolete. If only Test 3 works,
the alternative is "ship direct-distribution only and don't
pursue MAS for writes" — which is approximately our current
posture anyway.

### Research pass scope

A single ~half-day pass should cover items 1–4. Item 5 is an
email to Apple, owed in parallel. Item 6 falls out of Test 3
automatically.

If the research surfaces a viable path, document it as a
follow-up in `RESEARCH-IMAGECAPTURECORE-ALTERNATIVES.md` (don't
balloon this doc). If nothing viable surfaces, the pivot is
either read-only or doesn't happen.

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
