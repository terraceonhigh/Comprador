# PLAN — NFS READ path: JUKEBOX-on-threshold + async prefetch

> **Status:** drafted 2026-05-16 after the pcap analysis identified
> the bridge's synchronous full-file READ download as the cause of
> the user-visible "Server connections interrupted" alert. See
> [MISTAKES.md §NFS pivot entry 4](MISTAKES.md). Approach 1
> (Spotlight block via `.metadata_never_index`) handles the dominant
> user scenario and ships first. This plan covers the **second** fix
> that handles the rarer case of explicit phone→Mac file access
> from Finder.

## Motivation

After approach 1 lands, Spotlight stops pre-emptively reading every
file in a directory when the user enters it — eliminating the
"interrupted" alert on the dominant onboarding flow. But a real bug
remains: if the user double-clicks a large file on the phone to
preview it, or copies a large file from phone to Mac via Finder
drag, the same synchronous download in `bridge/nfs/cache.go`
will block until the entire file is on disk. For a 9 GB file at
21 MB/s USB-MTP this is ~7 minutes — far beyond macOS's NFS RPC
timeout. The user gets the same scary alert, just on a less common
trigger.

This plan is the second-phase fix that makes that case
fail-gracefully.

## Mechanism — NFS3ERR_JUKEBOX

NFSv3 status code 10008, `NFS3ERR_JUKEBOX`. RFC 1813 §2.6:

> NFS3ERR_JUKEBOX (10008): The server initiated the request, but
> was not able to complete it in a timely fashion. The client
> should wait and then try the request with a new RPC transaction.

Intended exactly for slow-media scenarios (the name dates from
optical-disc jukeboxes). NFSv3 clients are expected to retry with
exponential backoff. Returning JUKEBOX is the spec-blessed way for
a server to say "still preparing, ask again."

## Open question to probe before shipping

**What does macOS Finder actually do when our READ returns
JUKEBOX?** Three possible behaviours:

1. **Retry silently for some time, then succeed.** Best case. We
   serve a quick JUKEBOX while a background goroutine downloads
   the file; on the client's retry, the cache is populated and we
   succeed. User sees Finder's normal copy progress dialog.
2. **Retry briefly, then surface a "still preparing" indicator.**
   Acceptable. The user sees a non-scary indication that work is
   in progress.
3. **Surface a generic I/O error after one or two retries.**
   Worst case. Defeats the purpose. We'd fall back to a different
   approach (e.g. NFS3ERR_DELAY, or returning EAGAIN-equivalent).

**Probe test, before any production change:** add a one-line
override in the bridge that returns `NFS3ERR_JUKEBOX` on every
READ. Mount, open a small file on the phone via Finder
(double-click). Time how long Finder retries before either
succeeding (cache populates) or giving up. Note any user-visible
indication during the retry period.

Recommend running this probe in its own throwaway commit on a
short-lived branch — don't merge the always-JUKEBOX version.

## Design (assuming the probe shows behaviour 1 or 2)

### Threshold selection

A small file the bridge can download in ≪ NFS timeout (~20 s on
macOS) should still take the synchronous fast path. Below the
threshold we keep current behaviour; above the threshold we go
through the JUKEBOX-and-prefetch path.

At 21 MB/s USB-MTP, the bridge can complete a 100 MB file in ~5 s
and a 400 MB file in ~20 s. **Default threshold: 50 MB.** Tunable
via env var `COMPRADOR_READ_SYNC_THRESHOLD` for users with faster
USB 3.x devices who want to push it.

### Async prefetch state

Maintain a per-object download state in `downloadCache`:

```go
type cacheEntry struct {
    // existing fields...
    state    cacheState  // pending | downloading | ready | failed
    started  time.Time
}

type cacheState int
const (
    statePending cacheState = iota
    stateDownloading
    stateReady
    stateFailed
)
```

`cache.open` for a large file:
- If `state == ready`: return the `cachedHandle` as today.
- If `state == downloading`: return `NFS3ERR_JUKEBOX`. (Don't
  block on `entry.ready` — the whole point is to release the RPC
  goroutine fast.)
- If `state == pending`: transition to `stateDownloading`, kick
  off the async download via a new goroutine (or enqueue via
  `session.Do` from a worker pool), return `NFS3ERR_JUKEBOX`.
- If `state == failed`: return `NFS3ERR_IO` and clear the entry
  so the next retry starts fresh.

Small files (under threshold) keep the existing synchronous path
— the bridge can download the file inside the RPC timeout window
and respond with the bytes directly.

### Re-entrancy: keep MTP session ordering

The MTP session goroutine is a single serial worker. Background
prefetches enqueue requests via `session.Do` just like everything
else. Ordering preserved: if the user is also writing a file
during a prefetch, the writes interleave with read-chunk responses
on the session's request channel. No locking changes needed beyond
the cache state machine.

### Eviction policy

Existing `cacheEntryTTL = 30 * time.Second` already evicts unused
downloaded files. Extend to skip eviction of entries with
`state == downloading` (already partially done — the existing
`evictStale` checks `e.ready`). Confirm in code review.

## Sequencing

1. **Approach 1 (Spotlight block) lands first.** Independent
   change; addresses the dominant user-visible regression.
2. **Probe test for JUKEBOX behaviour.** Throwaway branch, do
   not merge. Record findings here.
3. **If probe shows behaviour 1 or 2:** implement this plan,
   ship as approach 2 for v0.4.0.
4. **If probe shows behaviour 3:** fall back. Options to consider:
   - Return `NFS3ERR_DELAY` instead (less standard but
     potentially less aggressive).
   - Block Finder from triggering reads on large files via a
     content-type heuristic.
   - Defer to the FUSE-T architectural pivot post-launch.

## Out of scope for this plan

- **True progressive read.** Would require either libmtp partial
  read support (doesn't exist; MTP protocol limitation) or
  asynchronous chunked-response semantics over a long-lived
  RPC sequence. Possible follow-up project.
- **Streaming media playback** (Quick Look video preview, etc).
  The 50 MB threshold means small previews work; large videos
  would be JUKEBOX'd indefinitely until the user explicitly
  pulls the file. Acceptable for v0.4.0; revisit if user
  feedback names this.
- **Negative caching.** If a file's download fails (libmtp error
  mid-stream, USB disconnect), we currently fail and clear the
  entry. Could cache the failure briefly to avoid retry storms,
  but premature optimization for v0.4.0.

## Verification

When this plan is implemented:

1. **Small-file pull** (under threshold): drag a phone-side file
   < 50 MB from `/tmp/comprador/...` to the Mac desktop via
   Finder. Should complete with a normal progress dialog, no
   alerts.
2. **Large-file pull** (over threshold): drag a 1 GB file from
   the phone to Mac. Finder should show "preparing" (or whatever
   the probe established), then complete via the prefetch cache.
3. **Spotlight stays blocked.** Approach 1's
   `.metadata_never_index` should ensure no spurious Spotlight
   reads during browse.
4. **Concurrent write during prefetch.** Drag a file Mac→phone
   while a phone→Mac prefetch is in flight. Both should complete;
   the write must not be starved.
5. **Cache eviction.** After 30 s of inactivity on a prefetched
   file, the temp file should be cleaned up.

## References

- [MISTAKES.md §NFS pivot entry 4](MISTAKES.md) — root cause
  empirical receipt + pcap analysis.
- RFC 1813, NFSv3 specification, §2.6 status codes.
- `bridge/nfs/cache.go` — current synchronous download
  implementation.
- `bridge/nfs/fs.go` — `MTPFileSystem.OpenFile` entry point.
- `bridge/vendor/github.com/willscott/go-nfs/nfs_onread.go` —
  upstream `onRead` handler; needs patching to thread the
  threshold + JUKEBOX return into the call site.
