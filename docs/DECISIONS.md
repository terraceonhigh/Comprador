# Decision Journal

Architectural decision points where more than one path was real. Future
contributors (and future-Mercer) should be able to see the alternatives
we considered and why we chose what we chose, without having to
reconstruct the reasoning from commit history.

Format: ISO date, short title, context, alternatives, choice, why,
consequences. One entry per decision.

---

## 2026-05-06 — Vanquishing the per-callback `VM_ALLOCATE` leak

**Context.** Mid-session profiling on an 8 GiB Mac during a 9.09 GiB
Attenborough.mkv MTP send showed the bridge process at 10.0 GB
physical footprint, with `vmmap -summary` attributing almost all of
it to **`VM_ALLOCATE`: 11.3 GB across 409 regions, 9.9 GB swapped
out**. The streaming-write refactor in `0c5a18e` and the F_NOCACHE
fcntl on staging-file reads worked correctly — but the binding
constraint turned out not to be the page cache (which F_NOCACHE
addressed) or the WebDAV-side `bytes.Buffer` (which streaming
addressed). It was the cgo callback layer.

`bridge/mtp/binding_callbacks.go`'s `goDataGetFunc` (libmtp's data
pull on uploads) and `goDataPutFunc` (libmtp's data push on
downloads) each call `make([]byte, int(wantlen))` on every
invocation. For a 9 GiB transfer at libmtp's ~22 MiB chunk size,
that's ~400 fresh slice allocations of ~22 MiB each. Go's GC frees
them; macOS's `MADV_FREE` policy retains them in our address space
until kernel reclaim; the 409 `VM_ALLOCATE` regions in `vmmap`
match the chunk count almost exactly. Receipt is in MISTAKES.md
entry 8a.

The leak persists per-bridge-process: each multi-GiB transfer adds
roughly its own size to the leak, and the only release is bridge
process exit. On low-RAM Macs this manifests as system thrashing
(load avg jumped from 3 to 41 during a 9 GiB GET).

**Alternatives considered.**

1. **Reuse a single `[]byte` buffer per callback session.** Hold
   the buffer in the registry entry alongside the io.Reader/Writer.
   First call: `make([]byte, max(wantlen, defaultChunk))`.
   Subsequent calls reuse; grow once if a `wantlen` exceeds
   capacity. Caps Go-side memory at one chunk (~22 MiB) per
   concurrent MTP operation. ~30 lines of code. Surgical.

2. **C-side buffer via cgo shim.** `C.malloc` the buffer in the
   SendFile/GetFile wrapper; pass its pointer to libmtp through a
   glue layer; `C.free` after the operation. Bypasses Go's heap
   entirely. Predictable C-heap behaviour. ~50 lines of code, more
   cgo dance.

3. **Force `runtime.debug.FreeOSMemory()` between transfers, or
   `GODEBUG=madvdontneed=1`.** Switches Go's reclaim policy to the
   more aggressive `MADV_DONTNEED` so the kernel returns pages
   immediately. Hides the symptom, not the cause; doesn't change
   the allocation pattern.

4. **Subprocess-per-transfer.** Spawn a transfer-worker process
   per MTP operation; let it die when done. The leak evaporates
   with the process. Bulletproof, but heavy: process spawn cost,
   complex orchestration, complicates device session lifecycle.

5. **Bridge auto-restart after N bytes transferred.** Comprador
   watches RSS, restarts the bridge when it crosses a threshold.
   Brute force, doesn't require touching libmtp or cgo.
   Last-resort caretaker pattern.

**Decision.** Path **#1** — buffer reuse via the callback registry.

**Why.** The intuition: the leak is exactly chunk-count of allocations
in the callback layer where the Go heap meets the C heap. If libmtp's
own internals were the leak source, we'd see allocations sized like
PTP packets (64 KiB) and many more of them, not 409 regions of
~22 MiB matching the callback chunk size. The callback is the
specific seam where our allocation pattern is wrong, and #1 fixes it
in place without architectural change.

#2 is a cleaner abstraction but doesn't pay for itself unless #1
turns out to be insufficient. #3 is a hidden-knob workaround. #4
and #5 are escape hatches that should remain on the shelf unless
the simpler approach demonstrably fails.

**Consequences.**

- **Expected:** bridge memory footprint goes from "approximately the
  file size" to "approximately one chunk" (~22 MiB) regardless of
  transfer size. Multi-GiB drags stop pushing low-RAM Macs into
  swap thrash.

- **Verification plan:** drag the same 9.09 GiB Attenborough.mkv
  on the same 8 GiB Mac. Sample `vmmap -summary` mid-MTP-send.
  Acceptance criterion: physical footprint stays under 1 GB
  (vs. the current 10 GB), `VM_ALLOCATE` regions count stays under
  ~50 (vs. 409).

- **Partial empirical confirmation (2026-05-11).** After a
  full ECON101 directory transfer through the bridge (432 files,
  49.6 MB total), the bridge process RSS sat at 8.4 MB and total
  VSZ at the Go-runtime baseline. Pre-fix arithmetic would have
  predicted ~50 MB of `VM_ALLOCATE` regions accumulated from the
  per-callback allocations across hundreds of WriteThrough cycles;
  the absence of that accumulation is direct evidence that buffer
  reuse is doing what it should. The full 9 GiB stress test
  remains the proper acceptance check (this was 49 MB across
  smaller chunks, not a single multi-GiB transfer), but the early
  signal is unambiguous.

- **If insufficient:** profile again, look for residual C-side
  allocations inside libmtp's PTP transaction layer; revisit #2
  (C-side buffer) or escalate to #4/#5 if libmtp itself is the
  source.

- **What this commit explicitly does not address:** the broader
  WebDAV/NetFS architecture risk (TODO.md "Reconsider the
  architecture if RAM is a binding constraint"). Even with the
  cgo-side fix, files larger than the available memory headroom
  on low-RAM Macs will still hit webdavfs's writeseq cap and
  surface Mode A. Phase 2 (companion-driven completion) handles
  Mode A invisibly when the companion's polling is up; the
  remaining cliff is documented separately.

**Proceeded:** path #1 implementation begins immediately after this
journal entry lands.

---

## 2026-05-11 — ImageCaptureCore investigation: declined as libmtp replacement

**Context.** [Letter 09](../correspondence/09-ptpcamerad-was-a-broker/letter.md)
identified that `ptpcamerad` is a userspace XPC broker, not a USB
exclusive-claim adversary, and proposed pivoting Comprador's USB
access path from libmtp + seizure-race + helper toward
ImageCaptureCore's `ICDevice` session API as a co-resident client of
the broker. The pivot promised to eliminate the kill-and-claim race,
the privileged helper, the DriverKit dext, and to make MAS
distribution plausible. Four empirical tests were sketched in
[RESEARCH-IMAGECAPTURECORE.md](RESEARCH-IMAGECAPTURECORE.md) to test
the proposal. Tests 1 and 2 ran on 2026-05-11.

**Empirical findings:**

- **Test 1 (coexistence):** PASS for PTP-mode devices. `ICDevice.requestOpenSession`
  returns `nil` while `ptpcamerad` is alive; the broker's PID is unchanged
  across the session lifecycle; Image Capture.app remains functional
  alongside our session. *But:* `ICDeviceBrowser` does not enumerate
  Android phones in File Transfer (MTP) mode — confirmed across Pixel 6
  (Google) and Sony Xperia 10 III (Sony). The Xperia exposes no PTP
  option in its USB-mode picker at all.
- **Test 2 (read throughput):** PASS. 19 MB/s sustained over 1.4 GiB,
  p99 chunk latency 215 ms, RSS flat at ~26 MiB throughout — no
  cgo-callback-style allocation leak.
- **The protocol-level scope ceiling:** PTP exposes camera content
  only (DCIM/Pictures). MTP — Microsoft's extension of PTP — is what
  exposes the general filesystem (Music, Downloads, app data) that
  Comprador exists to address. Per Test 2's catalog, all 2,351 items
  surfaced through ICCore on the Pixel were camera-roll videos.

**Alternatives considered.**

1. **Wholesale replacement of libmtp with ImageCaptureCore.** Letter 09's
   framing. Would eliminate the seizure race, the helper, the dext
   roadmap, and unlock MAS. Falsified by Test 1's MTP-invisibility
   finding — most users' phones are MTP-mode by default; for Sony,
   MTP is the only option.

2. **Dual backend, mode-aware** (libmtp for MTP-mode phones, ICCore
   for PTP-mode phones). Test 2 wrote this into its Conclusion before
   the scope-ceiling implication was named. Falsified by the
   protocol-level fact that PTP-mode phones don't expose non-camera
   content: even with both backends wired and Tests 3/4 passing,
   ICCore-routed users would lose access to Music/Downloads/app
   data — which is the use case Comprador exists for.

3. **Narrow opt-in: ICCore as a read-only fast-path for camera
   content specifically.** Some residual value (cleaner memory
   profile for photo-import use cases) but a small wedge of the
   product surface, duplicating what Image Capture.app already does
   natively. Not worth the code surface or the per-device mode
   negotiation.

4. **Decline the pivot.** Close the investigation, keep libmtp as the
   primary path, complete the architecture the established stack
   enables (multi-storage, multi-device per the existing plans).

**Decision.** Path **#4** — decline.

**Why.** ImageCaptureCore's scope ceiling (camera content) is upstream
of macOS, in the PTP-vs-MTP protocol distinction at the USB-interface
level. macOS reflects what the phone exposes; the phone exposes
camera content under PTP and the general filesystem under MTP. No
amount of macOS-side framework work changes that. Comprador's product
positioning is "phone as a general Finder volume" (per
[USER.md](USER.md)) — Music, Downloads, app data, the user's
non-camera content. ICCore can't address those regions of the
filesystem on any phone, regardless of mode, because phones decline
to expose them under PTP. The pivot was the right shape for a
hypothetical product whose scope was camera content; for Comprador's
actual scope it doesn't pay out.

#3 might one day become interesting if Comprador grows a
camera-content-specific feature surface (e.g., faster photo-import
mode), but YAGNI today. Tests 3 (writes via `requestSendPTPCommand`)
and 4 (sandbox/MAS compatibility) are skipped — they would measure
properties of a path whose architectural utility was already
foreclosed by the scope finding.

**Consequences.**

- **The libmtp path remains primary and load-bearing.** The seizure
  race, the privileged helper (slated for v0.4.0 removal per
  [SECURITY.md](SECURITY.md)), the DriverKit dext on the roadmap,
  and the cgo-callback buffer-reuse imperative (per
  [TODO.md](../TODO.md)) all retain their current status. Letter 09's
  optimistic catalogue of "wins this would unlock" should not be
  read as having unlocked anything.
- **The investigation receipts stay.** [RESEARCH-IMAGECAPTURECORE.md](RESEARCH-IMAGECAPTURECORE.md)
  retains the full Test 1 + Test 2 results, the scope-correction
  section, and the (unrun) Test 3 and Test 4 specifications for
  archival completeness. Future contributors evaluating a similar
  pivot have the empirical evidence in one place.
- **The probe binaries** at
  `bridge/cmd/ictest1/main.swift` and
  `bridge/cmd/ictest2/main.swift` are research-only — phony Makefile
  targets, gitignored output, not wired into any production build.
  Deletable in a single commit once the receipt in
  RESEARCH-IMAGECAPTURECORE.md is sufficient on its own.
- **Methodological lesson recorded in
  [letter 11](../correspondence/11-narrowing-the-pivot/letter.md)
  part three.** When a clean test result lines up with the
  architectural story you wanted to tell, check the *scope* of the
  evidence, not just its quality. Order candidate falsifications by
  how *broadly* they would invalidate the architectural claim, not
  by how *cheaply* they would.

**Proceeded:** the next work is completing the libmtp-side
architecture per
[PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md) and
[PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md), in that order, with
the cgo callback buffer-reuse fix
([TODO.md](../TODO.md) "Roadmap imperative") landing between them
to unblock multi-device.

## 2026-06-23: Expanding device compatibility

Three decisions on how Comprador reaches more devices, taken after the
compatibility page made explicit that support is decided by libmtp and the
macOS USB-claim layer, not by us.

**1. Keep libmtp current as a release practice.** Device support *is* libmtp's
per-device database; the bridge adds none of its own. The build copies whatever
`libmtp.9.dylib` Homebrew has installed, with no pinning. So the lever for "more
devices over time" is simply shipping a current libmtp: `brew upgrade libmtp`
before cutting a release. `BUNDLE_BRIDGE` in the Makefile now echoes the bundled
libmtp version so each build records what it embedded, and PRE-LAUNCH carries the
upgrade step. Declined a hard min-version gate as overkill for a solo release.

**2. Declined vendor-specific MTP auto-detection.** Auto-detection fires on a
known Android vendor ID OR a USB class-6 (Still Image / PTP) interface. The one
gap is a device exposing MTP only via a vendor-specific (class FF) interface that
isn't a known vendor. Detecting that robustly needs Microsoft OS String Descriptor
control transfers: fragile, complex, and rare. Decided not to build it; decision 3
covers the same gap with a deterministic, user-driven path instead.

**3. Added a manual "Look for a device" menu.** A submenu lists every attached USB
device; picking one force-connects it through the normal path
(`handleDeviceAttached` to `DeviceSession.connect` to the bridge's
`--device-loc-id`), bypassing the vendor/class filter, and libmtp does the real
test. The submenu repopulates on open via `NSMenuDelegate`, so it is live even for
a device auto-detection never reported. This covers the vendor-specific gap, a
pre-launch attach that raced, and reconnect-after-eject without a replug (the
manual action clears the post-eject suppression). Chosen over spawning the bridge
with `--device-loc-id=0`, which is non-deterministic when several devices are
attached.
