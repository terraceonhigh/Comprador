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
