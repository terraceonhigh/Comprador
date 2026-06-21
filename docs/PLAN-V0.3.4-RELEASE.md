# v0.3.4 release plan

Written 2026-05-18 night, after Step 3 (chunked prefetch, commit
`74702901`) landed on `claude/prefetch-redesign` and the yield test
passed. Architect's framing: C-only path, no rush, no hotfix
intermediate. v0.3.4 is the prefetch-redesign release.

This document tracks what is left between current `claude/prefetch-
redesign` HEAD and a tag-pushed GitHub release. **Edit freely as
work proceeds.**

## State at writing

- Last shipped tag: **`v0.3.2`** (2026-05-09)
- v0.3.3: retracted from GitHub Releases; local-only `v0.3.3-retracted`
  forensic anchor. `master` is still at the v0.3.3 merge.
- Branch with the actual fix: **`claude/prefetch-redesign`** (12 commits
  ahead of `claude/build-identity`).
- Cascade mechanism: **broken by construction** (Step 3 commits) and
  **empirically validated** via the yield test (`/tmp/test-step3-203247.log`,
  183 ms high-pri response mid-prefetch).

## Code work remaining (PLAN-PREFETCH-REDESIGN.md sequencing)

### Step 4 — Audit OpSendFile (Mac→phone writes) — DEFERRED to v0.3.5

**Decision:** defer; do not block v0.3.4.

`session.Do(OpSendFile, ...)` for a Mac→phone copy holds the libmtp
session for the full transfer duration — same shape as the original
prefetch bug. A 5 GB Mac→phone copy locks the session goroutine for
~3 min at USB-MTP rate.

**Why deferring is the right call for v0.3.4:**

OpSendFile is triggered exclusively by user-initiated writes, not
by Finder/Spotlight/QuickLook probing. The cascade pathology that
produced the 2026-05-18 morning required parallel-RPC amplification
during the prefetch window — which is the trigger condition for
the *read* side, not the write side. A Mac→phone copy that
incidentally coincides with a concurrent RPC storm against the
mount would still produce a session-lock, but no cascade has been
observed for this case and the trigger is deliberate (user is
*actively* writing) rather than ambient (Spotlight just is).
The lower spontaneity makes this a real-but-rare issue.

**Disclose in v0.3.4 release notes:** large Mac→phone copies still
hold the libmtp session for the full transfer duration. While
that copy runs, other phone operations may stall. The session
unblocks immediately when the copy completes. This is a known
limitation slated for v0.3.5 alongside the Finder copy-progress
regression.

**Step 5's soft-mount safety net catches this case anyway.** Even
if a Mac→phone copy somehow tipped into mount-down on a future
regression, `hard,intr` would surface EIO to the calling process
instead of cascading.

**For v0.3.5 (when Step 4 lands):** the implementation shape is
callback-side yielding via `LIBMTP_Send_File_From_Handler`'s
short-write contract — return less-than-requested every N bytes
and pull the high-pri queue between callback invocations. Different
shape from Step 3's chunk loop (which is request-side), same goal.

### Step 5 — Soft / interruptible mount option — independent, ship together

`bridge/nfs/server.go` (or wherever the mount-args are constructed)
currently passes `hard,nointr,timeo=10` (or similar) to
`NetFSMountURLAsync` via [MountManager.swift](../MenuBarApp/Sources/MountManager.swift).
Change to `hard,intr,timeo=30` or `soft`.

**Why this is the universal safety net:** Step 3 fixes *this* class
of bug. Step 5 catches *any future* fault that locks the bridge
long enough to mark the mount unresponsive — a libmtp pathology
we haven't seen, a deadlock in a different code path, a kernel-
side substrate issue. With `soft` (or `hard,intr`), the kernel
surfaces EIO to the calling process instead of cascading the wait.

**Risk:** `soft` mounts have historical reputation for "lost
writes if the server dies." Modern macOS NFS honors `intr` cleanly
for both reads and writes. Worth one engineering hour to validate
that a deliberate `kill -KILL bridge` mid-transfer surfaces a
clean EIO rather than data loss.

**Effort:** ~1 hour to ship + test.

### Step 6 — Strip per-RPC logging — independent, ship together

Commit `78eae7a3` added `[INFO] cache.open` and per-RPC log lines
that the morning's cascade .diag identified as contributing CPU
load via the Swift parent's `readabilityHandler` closure (92% CPU
in unified-log throughput during the cascade).

Strip back to:
- `cache.beginPrefetch START` / `END-OK` / `END-FAIL` (3 lines per
  prefetch)
- Error-only logging on the synchronous read path
- The Swift `readabilityHandler` itself audited — currently NSLogs
  every chunk through `cprLog`; that's fine for normal operation
  but could be the bottleneck during a future cascade-shape event

**Effort:** ~2 hours.

## Testing remaining

The yield test is the load-bearing falsifiable signal and it passed.
Below are the additional rounds to gain confidence before tagging.
Order matters — start with the cheap-and-high-signal ones.

### T1. Cascade-shape test on cold Spotlight (HIGH VALUE)

Reboot the Mac. Mount fresh against the Xperia. Drag a small file
into `Download/` (which contains Attenborough + the webm). The
2026-05-18 morning cascade fired exactly here. If step3 survives
this test cleanly, that's the empirical proof to complement the
by-construction claim.

**Cost:** one reboot + one drag, ~5 minutes wall-clock.
**Value:** the only test that produces the missing in-vivo evidence.

### T2. Multi-handle stress (MEDIUM)

Start two simultaneous large prefetches (open Attenborough in VLC,
then open another >50 MB file in another app). Verify both progress
without starvation, and that a high-pri operation lands in seconds
during the concurrent prefetch window.

**Cost:** ~5 minutes.
**Value:** catches "multiple low-pri requesters starve high-pri"
or "session goroutine deadlock under N > 1 low-pri" classes of bug.

### T3. Write-during-prefetch (MEDIUM)

Start an Attenborough prefetch. While it runs, drag a small file
from Mac→phone into a different directory on the mount. With
Step 3 only, this still serializes through OpSendFile — and
OpSendFile is high-pri by default — so the write should land in
the time it takes for libmtp to send 100 KB (~10 ms) plus one
chunk's yield latency (~600 ms).

**Cost:** ~5 minutes.
**Value:** confirms the OpSendFile path still works alongside the
new chunked OpGetPartial path. Catches "I broke something on the
write side while refactoring the read side."

### T4. Eject-mid-prefetch (LOW)

Start an Attenborough prefetch. Eject the mount via Finder
sidebar while the prefetch is running. Verify:
- The eject succeeds (or surfaces a clean error)
- The bridge does not crash
- A subsequent mount reconnect works

**Cost:** ~3 minutes.
**Value:** validates that Step 3's chunked loop tolerates abrupt
shutdown. The current code has no cancellation; the loop will
finish its current chunk and then the `session.Do` may block
forever if the bridge is shut down. Worth understanding the
failure mode before users hit it.

### T5. Multi-device round-trip (LOW)

Mount both Xperia and Pixel simultaneously. Start a prefetch on
one. Verify the other remains responsive.

**Cost:** ~5 minutes.
**Value:** confirms the per-device-session-goroutine isolation we
shipped in `claude/multi-storage` still holds with the priority
queue.

### T6. pjdfstest sanity (LOW)

Run the existing pjdfstest suite against a step3 mount.
**Cost:** ~10 minutes.
**Value:** catches POSIX-semantics regressions from the cache.go
refactor.

### T7. Long-uptime soak (DEFER)

Leave the bridge running for hours with the phone connected,
periodically probe with small operations. Verify no goroutine leak,
no file-descriptor leak, no memory growth.

**Defer:** valuable but not blocking; can ship and observe via
real-world use.

## Build + release work

### B1. Confirm the branch tip and rebase if needed

`claude/prefetch-redesign` is branched off `claude/build-identity`,
which is itself ahead of `master`. Before tagging:
- Decide whether `claude/build-identity` should merge to master
  separately (its own PR) or whether the whole stack lands in one
  PR.
- If separate: merge `claude/build-identity` first, then rebase
  `claude/prefetch-redesign` onto master.
- If together: rebase as-is or merge with the build-identity
  history preserved.

The architect's preference (per letter 15): build-identity's PR
title is overscoped; splitting was suggested. But the prefetch-
redesign branch building on top of build-identity creates a chain
that probably wants to land together unless there's a clean point.

### B2. CHANGELOG + release notes

Write `CHANGELOG.md` entry for v0.3.4 covering:
- **The cascade fix** (Step 3 chunked prefetch + Step 2 priority
  queue) — what bug it fixes, with the link to the v0.3.3-retracted
  receipt
- **The safety net** (Step 5 soft mount) — what it catches that
  Step 3 cannot
- **Logging strip** (Step 6) — performance + UX improvement
- **Build identity** — every CFBundleVersion now stamps the git
  hash; valuable for forensics
- **Harness fixes** (clean.sh strict gate, recover.sh path-with-
  spaces) — under-the-hood improvements
- **Known issues:** the Finder copy-progress regression
  ([TODO.md](../TODO.md) Pre-launch UX items) is unresolved and
  ships with v0.3.4. Disclosed in release notes; targeted for v0.3.5.

### B3. DMG build via existing notarization workflow

The notarization workflow shipped with v0.2.3 / v0.3.0 / v0.3.2.
- Update `RELEASE_VERSION` in the Makefile from `0.3.4-dev` to `0.3.4`.
- Tag: `git tag v0.3.4` then `git push origin v0.3.4`.
- CI builds the notarized DMG.
- Verify the DMG opens, the app launches, the bundle's
  CFBundleVersion stamps the release commit hash (clean, not
  `-dirty`).

### B4. GitHub release page

Title: "v0.3.4 — chunked prefetch + soft mount"
Body: condensed CHANGELOG with the cascade-fix story up top.
Attach: the notarized .dmg.

### B5. Post-release: close PR #24, retire `claude/changelog-v0.3.3`

PR #24 (claude/changelog-v0.3.3) was for the retracted v0.3.3. Close
without merging. Delete the branch.

## Go / no-go gates

v0.3.4 ships only if:

- [x] Step 3 in tree (commit `74702901`)
- [x] Yield test passes (high-pri OpListDir landed during prefetch;
      low-pri non-starvation: 183 ms for a 137 KB chunk request)
- [x] Step 4 decided: **deferred to v0.3.5** with release-notes disclosure
- [ ] Step 5 in tree (soft mount)
- [ ] Step 6 in tree (logging strip)
- [ ] T1 (cold-Spotlight cascade-shape) — empirical cascade-suppression
- [ ] T2 (multi-handle stress) — multiple concurrent prefetches
- [ ] T3 (write-during-prefetch) — OpSendFile path still works
- [ ] T6 (pjdfstest) — POSIX sanity
- [ ] CHANGELOG drafted (covering Step 4 + copy-progress as known
      issues with v0.3.5 disclosure)
- [x] Finder copy-progress regression: deferred to v0.3.5 with
      release-notes disclosure (see [TODO.md](../TODO.md)
      Pre-launch UX items)

T4 and T5 are nice-to-have; not blocking.

## Out of scope for v0.3.4

- Anything in [TODO.md "Pre-launch UX items"](../TODO.md) beyond
  the copy-progress regression — those block v0.4.0, not v0.3.4.
- File Provider migration / FUSE-T deliberation — long-running
  substrate question, separate track.
- Multi-device UX polish beyond what's already shipped.
- Step 4 (OpSendFile chunking) if decided to defer.
