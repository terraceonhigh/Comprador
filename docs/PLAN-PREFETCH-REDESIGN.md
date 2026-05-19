# Prefetch architecture redesign (scope C)

**Written 2026-05-18** after the v0.3.3 retraction, in response to the
cascading-Finder-freeze regression caused by the async prefetch
introduced in `a405ed48`. See [MISTAKES.md entry 4
"Empirical receipts"](MISTAKES.md), the 2026-05-18 16:51 reproduction,
and the three `.diag` files in `/Library/Logs/DiagnosticReports/`
for the receipts.

This document is a *working spec* for the architectural fix. Not
production doc — edit freely as work proceeds.

## What went wrong

### The bug in one paragraph

`a405ed48` added an async prefetch on JUKEBOX: when an NFS READ
arrives for a file > 50 MB, return JUKEBOX immediately and dispatch
a goroutine that calls `session.Do(OpGetFile, ...)` to download the
full file via libmtp in the background. The intent was that the NFS
client's retry within the JUKEBOX backoff window would find a
populated cache. **This works for the VLC scenario** (one client,
one file, no other I/O against the mount).

It is **pathological** for the Finder-icon-view scenario. The
single libmtp session goroutine in `bridge/mtp/session.go`
serializes ALL operations on the device — reads, writes, dirstats,
prefetches. When a prefetch begins, every subsequent NFS RPC queues
behind it. A 9 GB prefetch at ~27 MB/sec locks the bridge for ~6
minutes. During those 6 minutes, Finder's icon-view re-renders fire
parallel READs against directory members; each hits the same
session goroutine and queues; the kernel marks the mount "not
responding"; processes touching the mount path stall on the
`hard,nointr` mount.

### The leading indicator we missed

[MISTAKES.md entry 4 (2026-05-17 verification)](MISTAKES.md): *"the
architect observed they 'cannot browse other directories' during
the prefetch window — this is the existing within-device
concurrency limitation, not a regression introduced by the
prefetch."*

We dismissed this as "existing limitation." On Finder icon-view,
"cannot browse other directories" = **Finder freeze**, because
Finder is constantly issuing reads to render thumbnails. The receipt
was sitting in our own MISTAKES doc and we didn't connect the dots.

## Design constraints

1. **libmtp is single-session, single-cursor per device.** Per spec
   and per the cgo binding in `bridge/mtp/binding.go`,
   `LIBMTP_Open_Raw_Device_Uncached` does not support concurrent
   sessions on the same physical device — two open handles would
   collide on USB bulk transfers. So we cannot run prefetch on a
   second session goroutine.

2. **MTP transfers are inherently long.** A 9 GB file at USB-MTP rate
   (~27 MB/sec) is ~6 minutes. Any design that "holds the session
   goroutine for the whole transfer" recreates the current bug.

3. **NFS RPCs have a kernel-side patience window.** macOS NFSv3 hard
   mount: timeo=10 (1 sec), then exponential backoff up to ~60 sec.
   After ~30 sec of no response, the mount goes "not responding."
   So a single RPC waiting on the session goroutine has ~30 sec
   before the kernel cascade starts.

4. **Read-amplification on Finder browse.** Icon-view re-render fires
   one READ per directory member. A directory with 5 large files
   produces 5 simultaneous prefetch requests. The current design
   serializes them; even if we cap to 1 concurrent, the queue depth
   problem remains.

## Design space

### Option A — Chunked-yield prefetch with priority queue

The session goroutine accepts two priority lanes: high (real NFS
RPCs) and low (prefetch chunks). Prefetch is split into ~4-16 MB
chunks. Between chunks, the goroutine drains the high-priority
queue before pulling the next prefetch chunk.

**Mechanics:**
- `session.Do` enqueues at priority normal
- `cache.beginPrefetch` enqueues a *meta-request* — "download object
  X in chunks." The meta-request expands into N chunk requests at
  priority low.
- Session goroutine pseudocode:
  ```
  for {
    if high-priority msg available, process it
    else pull one low-priority msg, process it
  }
  ```
- A prefetch chunk that hits libmtp's `LIBMTP_Get_File_To_Handler`
  reads ~4 MB then returns to the goroutine main loop.

**Cost:** libmtp doesn't directly support resumable / chunked reads.
We'd need to either (a) use `LIBMTP_GetPartialObject` repeatedly with
incrementing offsets, or (b) abuse the handler-callback to break out
at chunk boundaries (the handler returns -1 to abort; we'd lose
in-flight bytes and resume from offset). Option (a) is cleaner but
slower (each partial-object is a fresh MTP transaction with setup
overhead).

**Latency:** A high-priority RPC arriving mid-prefetch waits for the
current chunk to complete. At 4 MB chunks / 27 MB/sec = ~150 ms
worst case. Acceptable.

**Effort:** 2-3 days. Touches `session.go`, `cache.go`, and the
libmtp binding layer.

### Option B — Cancellable prefetch with restart-on-retry

Prefetch holds the session goroutine for its full duration, but the
moment any real NFS RPC arrives, the prefetch aborts (closes the
libmtp transfer), the RPC is serviced, and the prefetch is
re-enqueued at the tail. The next JUKEBOX retry from the NFS client
restarts the prefetch from the beginning.

**Cost:** Wasted bandwidth (re-download from offset 0 each time).
A 9 GB file may never complete if Finder keeps generating browse
RPCs.

**Effort:** ~1 day. But the wasted-bandwidth problem makes this
unviable for Comprador's USB-MTP-bound use case.

### Option C — Prefetch only-when-idle

`session.Do` is enqueued normally. `cache.beginPrefetch` checks the
queue depth: if any other request is pending or in-flight, **don't
dispatch the prefetch at all**, just return JUKEBOX. The prefetch
only runs when the session goroutine is truly idle (no pending NFS
RPCs).

**Mechanics:** Add a `pendingDepth` counter to the session. Increment
on enqueue, decrement on completion. `beginPrefetch` checks
`atomic.LoadInt32(&pendingDepth) == 0`; if not, return without
dispatching.

**Cost:** Prefetch rarely runs on a busy mount. The "VLC opens 9 GB
file" scenario only completes if the user stops doing anything else
through the mount for the full ~6 minutes.

**Effort:** ~1 day. Simple and surgical.

**Downside:** Defeats the prefetch's purpose for the busiest case.

### Option D — Chunked-yield with cooperative checkpoints (recommended)

Hybrid of A and C, leaning toward A. Prefetch is chunked using
`LIBMTP_GetPartialObject` for 16 MB chunks (see "Empirical findings"
below for the chunk-size derivation). The session goroutine runs a
simple priority pump (A). Additionally: before pulling the next
prefetch chunk, the session goroutine sleeps briefly if the
high-priority queue has been empty for less than 100 ms — this
"smooths" busy periods where RPCs arrive in clumps.

**Effort:** 3 days. The chunked-libmtp work is the long pole.

## Empirical findings (probe run 2026-05-18 17:55 — 18:08)

`bridge/cmd/prefetch-probe` ran against both target devices. The three
questions in the original "Open empirical questions" section have hard
answers now.

### Q1: Does `LIBMTP_GetPartialObject` work on Xperia + Pixel?

**Yes on both.** `LIBMTP_DEVICECAP_GetPartialObject` advertises `true`
on Sony Xperia XQ-BT52 (VID 0x0fce, PID 0x520d) and Google Pixel 6
(VID 0x18d1, PID 0x4ee1), and the calls actually succeed against
Attenborough.mkv (9 GB) and Genki I.azw3 (135 MB) respectively. No
fallback to handler-callback needed for Comprador's current target
hardware.

### Q2: What's the per-chunk overhead?

| Phone | Chunk size | Median chunk | Mean chunk | Max chunk | Throughput | vs full-read |
|---|---|---|---|---|---|---|
| Xperia | 4 MB | 143 ms | 157 ms | 299 ms¹ | 25.4 MB/s | 30.6 MB/s, ~17% slower |
| Pixel | 4 MB | 148 ms | 151 ms | 178 ms | 26.4 MB/s | 29.8 MB/s, ~11% slower |

¹ Single outlier on the Xperia, chunk 13. Probably USB jitter; no pattern.

**Per-chunk fixed overhead is ~17 ms.** Derived from: 4 MB transfer at
30 MB/sec = 133 ms of bytes-on-wire; total chunk time ~150 ms; delta
~17 ms is the MTP-transaction setup. This is the load-bearing number
for chunk-size selection.

### Amortization math — why 16 MB is the chunk size

The setup cost is per-call, not per-byte. Larger chunks amortize it:

| Chunk size | Transfer time @ 30 MB/s | + 17 ms setup | Setup overhead % | Yield latency (worst-case) |
|---|---|---|---|---|
| 4 MB | 133 ms | 150 ms | **11%** | 150 ms |
| 8 MB | 267 ms | 284 ms | 6% | 284 ms |
| **16 MB** | **533 ms** | **550 ms** | **3.1%** | **~600 ms** (with jitter) |
| 32 MB | 1067 ms | 1084 ms | 1.6% | 1100 ms ⚠ |

**Chosen chunk size: 16 MB.** Sweet spot between:

- **Amortization:** 3% overhead is acceptable; full reads are the
  upper bound (zero overhead) and we want to stay within a few % of
  them.
- **Yield latency:** ~600 ms worst-case wait for a high-priority RPC
  is *below* macOS NFSv3 client's `timeo=10` (1 second) first-timeout
  budget, so a Finder browse during prefetch will see at most one
  chunk's latency, not a kernel-side retry.
- **Avoided regime:** 32 MB chunks tip into 1100 ms worst-case, which
  crosses NFS's first-timeout boundary. Even if our retry handling is
  correct, we'd be inviting kernel-side timeout-retry cascades that
  *might* surface as cosmetic "Server connections interrupted"
  alerts.

The amortization breakeven is generous: chunk sizes from 8 MB to 24
MB are all defensible. 16 MB is the round-number midpoint that's
easy to remember and reason about. Encode as a tunable constant
`prefetchChunkSize` in `cache.go` with a comment pointing at this
section.

### Q3: Does libmtp serialize partial-object calls correctly?

**Yes.** Sequential calls with increasing offsets at the same object
ID work without state rebuild. Per-chunk wall times stay within a
narrow band across the run (143–299 ms on Xperia, 140–178 ms on
Pixel) — no monotonically-increasing pattern that would suggest
libmtp is re-doing per-call setup.

### Cascade-fix math (what this buys us)

Current behavior on the v0.3.3 reproduction: prefetch locks the
libmtp session for the full Attenborough download (~5 min). Every
NFS RPC during that window queues. The kernel marks the mount "not
responding" after ~30 sec of unanswered RPCs. Finder cascades.

With chunked-yield at 16 MB: each chunk locks the session for ~600
ms. After each chunk, the priority pump drains queued high-priority
RPCs (each ~single-digit ms). **A Finder browse during prefetch sees
~600 ms lag once per chunk, then immediate response.** Order-of-
magnitude improvement; well within human-tolerable interactivity.

### Bonus observation (out-of-scope for this plan)

The Pixel's `LIBMTP_Get_Files_And_Folders(storage=65537, parent=0x0)`
took **3 min 31 sec** to enumerate the storage root, vs the Xperia's
instant return. Same call, both freshly-opened MTP sessions. Likely a
Pixel-specific quirk (many objects at root, or libmtp doing per-object
GetObjectInfo serially for thousands of objects).

Not a prefetch concern — affects first-mount-after-replug enumeration
latency. **Worth filing as a separate investigation item** before any
release that wants Pixel-equality with the Xperia for first-browse
responsiveness.

## Plan

### Step 1 — Empirical probe (✓ done 2026-05-18)

Built `bridge/cmd/prefetch-probe/main.go` (commit `32ee45cd`). Ran
against Xperia XQ-BT52 (Attenborough.mkv, 9 GB) and Pixel 6 (Genki
I.azw3, 135 MB). Findings captured in the "Empirical findings"
section above. Verdict: **chunked-yield viable on both phones, 16 MB
chunks chosen for the production design.**

### Step 2 — Session priority queue (1 day)

Modify `bridge/mtp/session.go`:
- Add `priority` field to `MTPRequest`
- Replace the single request channel with two channels: `highPri`
  and `lowPri`
- Run-loop drains `highPri` first; pulls from `lowPri` only when
  `highPri` is empty
- Existing call sites all use `priority: normal` (high); no behavior
  change yet

### Step 3 — Chunked prefetch (1-2 days, depends on Step 1 outcome)

Rewrite `bridge/nfs/cache.go`'s `download(...)`:
- If Step 1 said chunked is viable: split into N partial-object
  calls, each enqueued at `priority: low`
- If not viable: use the handler-callback approach (return -1 from
  the callback every N bytes; the session goroutine then checks the
  high-pri queue before resuming)
- Either way: the prefetch yields the session goroutine between
  chunks

### Step 4 — Audit other long-running ops (half day)

`session.Do` for OpSendFile (Mac→phone write) is also potentially
multi-minute. Does it block all other RPCs? Likely yes, same as
prefetch. The chunked-yield treatment should apply.

The libmtp send path uses `LIBMTP_Send_File_From_Handler`. The
handler callback approach (return short writes, let the session
goroutine yield) is the path.

### Step 5 — Soft / interruptible mount option (parallel, can ship first)

Independent of the prefetch redesign:
- Change the mount options Comprador passes via `NetFSMountURLAsync`
  or `mount_nfs` from `hard,nointr` to `soft` or `hard,intr,timeo=30`.
- Even with the prefetch redesign, ANY future bridge fault should
  not cascade-freeze the system.
- See `MountManager.swift` for the mount-call site.

### Step 6 — Strip per-read instrumentation (parallel, can ship first)

Commit `78eae7a3` added log lines on every cache.open and READ
decision. Production should not log per-RPC. Strip back to:
- Prefetch START / END / FAIL (one line each)
- Errors only on the synchronous read path
- The Swift parent's stderr `readabilityHandler` should also be
  audited for its NSLog-per-chunk + substring-match-3x behavior; at
  high stderr rates it pegged the dispatch queue at 92% CPU.

## Test plan

### Reproduction targets (must all pass)

1. **Finder icon-view of a directory with Attenborough + 139 MB doc**
   does not freeze the system. Browsing other directories during a
   prefetch works.
2. **Mac→phone drag of a small file** into the same directory
   completes in normal time (a few seconds) regardless of whether a
   prefetch is in flight.
3. **VLC opens the 9 GB Attenborough.mkv** with the same ~6 min wait
   as today (no regression on the VLC scenario the original
   prefetch was for).
4. **Multi-device** — Xperia + Pixel both mounted, both responsive
   during a prefetch on one of them.
5. **Bridge fault simulation** — `kill -KILL bridge` while the mount
   is in use. Finder should show an error within ~30 sec (mount
   becomes EIO on operations against it), not cascade-freeze.

### Acceptance criteria

- Bridge stderr rate < 10 lines/sec during normal use (down from
  hundreds/sec under prefetch instrumentation)
- No system freeze under any reasonable reproduction
- No regression on shipping behaviors (per-storage quota,
  phone-side change reflection, AppleDouble filter, multi-device)
- pjdfstest still passes (sanity)

## Open questions for the architect

1. **Release shape.** Two flavors:
   - **A+B-then-C**: Ship a v0.3.4 hotfix with just Step 5 (mount
     option) + Step 6 (logging strip) + flip the prefetch flag off
     (revert to JUKEBOX-only). Restores a working release fast. C
     lands as v0.4.0.
   - **C-only**: Skip the hotfix. Spend 3-5 days. Ship as v0.3.4.
     Cleanest history; `master` ahead of latest tag the whole window.
2. **Backwards-compat for the prefetch config.** Worth a runtime
   flag `--enable-prefetch=true/false` so we can ship Step 5 + Step
   6 + prefetch-off-by-default as a hotfix, then flip the default
   back on when C lands.

## Status

Plan written 2026-05-18 evening. **2026-05-18 (later evening): architect
picked C-only.** Framing: *"Comprador currently has 0 stars on github,
we have time to get C working and Comprador be a respectable piece of
middleware. There is no rush to hotfix."* Step 1 (empirical probe)
is the next concrete action. Master stays unreleased above v0.3.2 for
the duration of C's work; the v0.3.3 retraction is the last public
state.
