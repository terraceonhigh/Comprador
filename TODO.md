# Comprador — TODO

## ⚠ NEXT SESSION — start here

### PIVOT (2026-06-07): v0.3.4 prefetch is PARKED — we're moving to Galatea.

The Architect chose to **pivot to Galatea now** (Phase 4 of the original
charge). Galatea's userspace NFSv4 substrate landed and is real: a 130 s
READ completes exit-0 (R1), a 1 GB write→remount→read is byte-identical (R7),
live headless read-write mount with no root, conformance suite green. That
substrate has no RPC-timeout window — which means **the entire prefetch
redesign exists to dodge a constraint that no longer exists.** So no further
polish goes into it; the v0.3.4 plan below is parked, not deleted (the
receipts stay as history and as the acceptance spec the new substrate must
keep passing).

**Branch:** `mercer/galatea-integration`. **Target:** v0.4.0 on Galatea.

**Done this session (commit `99e6020f`):** the Phase-4 dry-fit —
`bridge/mtpfsal/` implements Galatea's `pkg/virtual` FSAL (Directory/Leaf/Node)
over `*mtp.Session`, compiling green against the **public** Galatea module
(`v0.1.0-alpha`). **Sourcing — verified 2026-06-07:** the public release
exposes `pkg/virtual` (the FSAL interface — the dry-fit builds against it), but
does **NOT** contain the root `galatea.Serve` entry point. `Serve` was added
post-release (Galatea DEC-022) and lives only on the unpushed canonical branch
`claude/unruffled-dijkstra-7f1e6d`. So the **server-cutover step (2) below needs
the canonical code**, via one of: (a) the Architect pushes Galatea's canonical
branch and we pin to it, or (b) a local `replace github.com/terraceonhigh/galatea
=> /Users/terrace/Labs/Galatea/.claude/worktrees/unruffled-dijkstra-7f1e6d` in
`bridge/go.mod`. **Resolved (2026-06-07):** the harness uses a separate
`bridge/galatea.mod` carrying the canonical require+replace, so production
`bridge/go.mod` stays pristine. (The build tag is gone; reads are proven —
see below.)

### READ PATH DONE — LIVE-PROVEN on a Pixel 6 (2026-06-07, commit `ac6b5193`).

`galatea.Serve` backed by `bridge/mtpfsal` mounts on macOS (headless, no root,
vers=4.0), browses the full Android tree, and reads files correctly with **no
JUKEBOX**. Receipts: a 3.1 MB JPEG byte-exact + md5-stable across reads; a
**95 MB mp4 (1.9× the old 50 MB JUKEBOX threshold) streamed clean in 17 s,
exit 0** — the willscott path would have refused it with NFS3ERR_JUKEBOX.
Run it: `make galatea-dev` → note `PORT=` → `make galatea-mount PORT=N` →
browse/read `/tmp/galmnt` → `make galatea-umount`. Build plumbing: harness is
`bridge/cmd/galatea-serve` built via the separate `bridge/galatea.mod`
(production `bridge/go.mod` stays pristine; `go mod vendor` is never run because
it would clobber the vendor-only-patched go-nfs).

Two live bugs fixed (both in the commit): NFSv4 OPEN lands in
`VirtualOpenChild` (not OpenSelf), so a blanket-ROFS stub returned EROFS on
every read; and `ChangeID` is a mandatory GETATTR attribute (server panics if
requested-but-unset — the M-006 lesson).

**Next increment — WRITES (needs the phone):**
1. Extract `writeRegistry` + sentinels + AppleDouble filter from `bridge/nfs`
   to a **neutral package** (e.g. `bridge/staging`) so `mtpfsal` never imports
   `nfs` (wrong-direction coupling that would block deleting go-nfs).
2. Flesh `mtpfsal` writes through `(*mtp.Session).Do` (libmtp isn't
   thread-safe; Galatea calls the FSAL concurrently across NFSv4 open-owners —
   the one-cursor seam, Galatea `Correspondance/04`):
   - `VirtualOpenChild(create)` + `VirtualWrite` + `VirtualClose`: staged temp
     + idle-flush commit via `OpSendFile` (port `write.go`).
   - `VirtualSetAttributes(truncate)`, `VirtualMkdir` (`OpCreateFolder`),
     `VirtualRemove` (`OpDelete`), `VirtualRename` (copy+delete — no native MTP
     rename).
3. **Throughput tuning** (read path works but ~5.6 MB/s vs prefetch-probe's
   21 MB/s): small NFS rsize → many per-chunk `OpGetPartial` calls. Try a
   larger advertised rsize / read-ahead coalescing. Also do a literal >60 s
   MTP read once a big-enough file is available (Galatea R1 already proved
   130 s at the protocol layer).
4. Wire into `main.go` behind a flag (probe-bind for the port — `galatea.Serve`
   has no listener injection; an interface-flex noted to Daedalus), keep the
   `PORT=`/`HOST=`/`DEVICE=` stdout protocol + Swift mount side unchanged;
   confirm v3→v4 NetFS/`mount_nfs` option deltas. Resolve the production vendor
   story here (manual-vendor galatea, or fork go-nfs to a local replace so
   `go mod vendor` is safe).
5. **Prove read-write live under load**, THEN delete `bridge/nfs/cache.go`
   (JUKEBOX + prefetch) and the patched go-nfs fork. Prove-then-delete.

The reply opening Phase 4 is delivered to Daedalus's mailbox:
`~/Labs/Galatea/Correspondance/04-phase-four-and-the-one-cursor/` (uncommitted
in Galatea's repo — commit/push there is the Architect's hand). It poses three
seam questions (open-owner sequencing vs. a global funnel; the uint32-handle
gift; where MTP's reality will want the interface to flex).

---

### PARKED — v0.3.4 prefetch release (pre-pivot state, kept as history)

State as of **2026-05-18 night** (Step 3 landed, yield test passed,
cascade fix verified by construction + empirically via the
discriminating yield test).

**Live spec for the parked stretch:** [`docs/PLAN-V0.3.4-RELEASE.md`](docs/PLAN-V0.3.4-RELEASE.md).
It carries the go/no-go gates for the v0.3.4 release, the
remaining test rounds, and the build/tag work.

**Where the cascade fix stands:** the v0.3.3 cascade *as observed*
is fixed. Step 3 (chunked prefetch at PriorityLow, commit `74702901`)
breaks the session-goroutine-locked-for-minutes link of the cascade
chain by construction. The yield test on 2026-05-18 ~20:41 measured
183 ms for a 137 KB high-pri read while a 9 GB low-pri prefetch was
running — well inside the kernel-side mount-down threshold. The
*class* of cascade is one ship away from being impossible: Step 5
(soft/interruptible mount, ~1 hour of work) catches any future
fault regardless of cause.

**Canonical receipts:**

- [`docs/PLAN-V0.3.4-RELEASE.md`](docs/PLAN-V0.3.4-RELEASE.md) — live
  release spec, go/no-go gates, remaining test rounds (T1–T7).
- [`docs/MISTAKES.md`](docs/MISTAKES.md) entry 4 — full receipt of
  the cascade investigation arc, including the step2 control,
  prod control, step3 yield-test, and the "is v0.3.3 fixed"
  framing with caveats.
- [`docs/PLAN-PREFETCH-REDESIGN.md`](docs/PLAN-PREFETCH-REDESIGN.md) —
  the original working spec for Scope C. Steps 1, 2, 3 done; Steps
  4 (decide in/defer), 5 (soft mount), 6 (logging strip) remain.
- [`correspondence/15-the-day-the-harness-bit-twice/letter.md`](correspondence/15-the-day-the-harness-bit-twice/letter.md) —
  the post-cascade reflection that framed the methodological
  lessons. Still load-bearing for harness discipline; the
  by-construction fix did not invalidate any of letter 15's
  framings, only added a positive empirical receipt.

**Next concrete moves, in order:**

1. **Step 5 — soft/interruptible mount.** [MountManager.swift](MenuBarApp/Sources/MountManager.swift).
   ~1 hour. Universal safety boundary; ships with v0.3.4 regardless.
2. **Step 6 — strip per-RPC logging.** ~2 hours. Removes the
   stderr-firehose CPU load that contributed to the morning's
   cascade footprint.
3. **Decide on Step 4 (OpSendFile chunking).** In v0.3.4 or
   deferred to v0.3.5 with disclosure. See the release plan.
4. **T1 cold-Spotlight cascade-shape test** (one reboot + one
   drag). Produces the in-vivo cascade-suppression evidence the
   yield test is a proxy for.
5. **CHANGELOG + tag + DMG + GitHub release.**

**Branches currently in play:**

- `master` — at `v0.3.3` merge (`8f818bc1`), unchanged since the
  retraction.
- `claude/changelog-v0.3.3` (PR #24) — stale CHANGELOG for retracted
  v0.3.3. **Close.**
- `claude/build-identity` (PR #25, 10 commits) — build-identity
  stamping + harness + cprLog conversion. **Should rebase into
  the v0.3.4 release path** (or merge first, depending on review
  preference — see release plan B1).
- `claude/prefetch-redesign` (this branch, ahead of build-identity)
  — Steps 2 + 3, harness fixes, all of today's durable work.
  **This is the branch v0.3.4 ships from** after Steps 4/5/6 land
  and tests pass.

---

## Current state — end-of-stretch 2026-05-17

**Two pre-launch blockers cleared in this stretch:** the NFS READ
stall (multiple framings, eventually fixed at the application
layer) and multi-device support (steps 4 → 6 of
[PLAN-MULTI-DEVICE.md](docs/PLAN-MULTI-DEVICE.md) shipped). The
branch is now ~55 commits ahead of master.

### What landed (chronological, all on `claude/multi-storage`)

**NFS READ stall, 2026-05-16 → 17:**

| Commit | What |
|---|---|
| `9239dcd7` | Revert `0d1418ac` fileSync-hold — UX falsified for >600 MB |
| `fc6a1799` | Root cause via pcap: bridge silently drops every NFSv3 READ |
| `ce4e7fb6` | `docs/PLAN-NFS-READ.md` for JUKEBOX approach |
| `56c44372` | `.metadata_never_index` sentinel (Spotlight block) |
| `1acdf7f7` | `NFS3ERR_JUKEBOX` on READ for files > 50 MB |
| `a405ed48` | Async prefetch on JUKEBOX (+ stripped leftover `[INFO] WRITE`) |

Verified end-to-end: VLC opens a 9 GB phone-resident video in
~6 min instead of hanging forever; small-file drags land in
2–3 s; QuickLook icon-view alerts reduced from "stacking 5+" to
"at most one cosmetic flash."

**Multi-device support, 2026-05-17 afternoon:**

| Commit | What |
|---|---|
| `d21dd133` | Bridge `--device-loc-id` flag + IOKit Location ID reconstruction via libusb (step 4) |
| `22d2b7b1` | Swift `BridgeProcess.start(locationID:)` + AppDelegate guard relaxed (step 5) |
| `a135ff4f` | Menu shows one block per attached device (step 6) |
| `db50d540` | `cleanupStaleMounts` recognizes per-device `.local` NFS sources (regression fix) |

Verified end-to-end with the Xperia XQ-BT52 + Google Pixel 6
plugged in simultaneously: menu bar app spawns two bridges,
each claiming its own phone, each with its own mDNS hostname
and mount path. Cross-device drag-drop (Xperia SD card → Pixel
Internal, and vice versa) works cleanly.

**UX polish:**

| Commit | What |
|---|---|
| `486af7d9` | Build menu item is now clickable — copies `BuildInfo.id` to clipboard |

### What's open

In rough order of priority for the v0.4.0 launch story:

1. **Pre-launch UX items** in the section further down this
   file — user-facing disclosure of `ptpcamerad` kill, etc.
   Some are blocked on Pinterest moodboard research that the
   architect mentioned post-letter-13.

2. **PR shape decision on `claude/multi-storage`.** Now ~55
   commits ahead of master. Each commit is independently
   reviewable; letters 12, 13, 14 give the chronological
   summary. PR + merge at the architect's pace.

3. **Multi-device step 7 — `USBSeizer.shared` batching.** Per
   [PLAN-MULTI-DEVICE.md §7](docs/PLAN-MULTI-DEVICE.md). When
   two phones plug in within ~100 ms, both DeviceSessions fire
   `killall ptpcamerad` redundantly. The current behavior is
   correct but noisy; a 200 ms batching window would suppress
   the duplicate kill. Not blocking — bridges still claim
   their devices fine; just wasteful.

4. **FUSE-T deliberation** (originally queued post-launch).
   The NFS READ fix substantially closed the gap that
   motivated FUSE-T as a substrate replacement. Re-evaluate
   after v0.4.0 ships and user feedback names the residual
   pain points (if any).

### Tidying followups

Small items collected during this stretch:

- **`dist-swiftc` inherits `-D DEBUG`** from `app-swiftc`, so
  production builds expose the debug menu items (Synthetic
  Flutter, Build identifier with copy-on-click). Separate
  the debug flags between the two targets.
- **`BuildInfo.swift` regeneration trigger.** Currently the
  Makefile reads `git rev-parse --short HEAD` at the start of
  the build, but if the working tree changes (commits added)
  between that read and the binary launch, the embedded ID
  can lag. Footgun confirmed 2026-05-17 (post-commit launch
  showed the previous build's HEAD). Either regenerate
  BuildInfo.swift on every `app-swiftc` invocation
  unconditionally, or stamp the binary post-link.
- **`make app` (xcodebuild path) is broken** on pbxproj drift
  (DeviceSession.swift + BuildInfo.swift not in the project
  file — MISTAKES 23a). `make app-swiftc` is the working
  path; either fix the pbxproj or retire `make app`.

---

## Earlier NEXT SESSION blocks (preserved for context) — all shipped 2026-05-16 / 17

**Pre-launch blocker for v0.4.0.** Root cause identified
2026-05-16 afternoon via pcap analysis (see
[MISTAKES.md §NFS pivot entry 4](docs/MISTAKES.md)).
**TL;DR:** the bridge silently drops every NFSv3 READ RPC
because `cache.open()` synchronously downloads the entire
MTP file before responding. When Finder enters a directory,
macOS Spotlight issues parallel READs against every file in
it for thumbnail/preview indexing; the bridge's first
multi-GB download holds the read path for minutes; macOS
times the RPCs out and surfaces "Server connections
interrupted" to the user. Ships in every v0.2.x and v0.3.x
release.

**Earlier mis-attributions (preserved as receipts):**
- Mis-attributed to commit `0d1418ac` (fileSync-hold).
  Reverting in `9239dcd7` did not help. Revert still correct
  for separate reasons (MISTAKES entry 3).
- Mis-attributed to "substrate issue" after `00235ca`
  reproduced. Wrong framing — the bug is application-layer.

**Fix shipping order (recommended):**

1. **Block Spotlight indexing at the mount root.** The
   first-and-only thing a fresh-mounted user does is open
   the phone in Finder. That triggers Spotlight indexing on
   every file in the directory, which triggers the stall.
   Drop a `.metadata_never_index` file at the NFS root (or
   serve it virtually) so macOS skips the mount entirely.
   This alone eliminates the user-visible "interrupted"
   alert on the dominant scenario. Cost: a few lines in
   `bridge/nfs/fs.go` or `cache.go`.

2. **Return `NFS3ERR_JUKEBOX` on READ for files above some
   threshold** (configurable; default e.g. 50 MB). NFSv3's
   "media not ready, retry later" status — RFC 1813 §2.6.
   Finder honors it as a polite "still preparing" indicator
   rather than a connection failure. This makes the rare
   case of opening a large file from the phone fail
   *gracefully* rather than hang. Cost: handler-level
   threshold check in `bridge/nfs/fs.go`'s `OpenFile`, plus
   the JUKEBOX status code in our error mapping.
   Open question: confirm Finder doesn't escalate JUKEBOX
   to a user-visible warning after N retries — needs a
   probe test before shipping.

3. **(Out of immediate v0.4.0 scope.)** True progressive
   read — would need either libmtp partial-read support
   (doesn't exist in libmtp) or asynchronous download +
   chunked response over a long-lived RPC sequence.
   Possible follow-up project.

**FUSE-T deliberation context.** FUSE-T would also resolve
this class entirely (the FUSE write/read callback model has
no equivalent kernel-side RPC timeout). But approaches 1
and 2 above are days of work each and ship-blocking
fix-grade; FUSE-T is a week+ substrate replacement.
Recommended sequencing: **land 1 + 2 for v0.4.0**, then
deliberate FUSE-T post-launch as a clean architectural
improvement rather than an emergency.

**Verification protocol** for the eventual fix:
1. Drop a small file plus a large file (>500 MB) into the
   phone's `Download/` via `adb push`, to seed indexable
   content.
2. Mount fresh. Open `/tmp/comprador/<storage>/Download/`
   in Finder. **Expected:** no "Server connections
   interrupted" alert appears within 60 s.
3. Drag a small file *into* the directory. **Expected:**
   Finder progress dialog appears immediately, dismisses
   on commit, no alert.
4. Spot-check a phone→Mac pull of a small file (<10 MB).
   **Expected:** completes within seconds via Finder copy.
5. Spot-check a phone→Mac pull of a large file (>500 MB).
   **Expected with approach 2 active:** Finder shows a
   "preparing" or retry-style indication, eventually
   succeeds.

**Investigation artifacts (preserved):**
- `build/stall.pcap` — full lo0 NFS traffic during session 5.
- `build/pcap_analyze.py`, `build/pcap_dissect.py`,
  `build/pcap_rpc.py`, `build/pcap_read_args.py` — pure-stdlib
  Python analyzers, reusable for future NFS-layer probes.
- `build/dev-nfs-2026-05-16.log` (sessions 1+2),
  `build/dev-nfs-2026-05-16-post-reboot.log` (session 3),
  `build/dev-nfs-stall-probe.log` (session 5).
- Session 4 (v0.3.1) — log path TBD; architect ran the test, no
  preserved artifact path recorded yet.

---

## Navigation — where work lives

This file is the central backlog. Several adjacent docs track
specific kinds of work that don't belong here verbatim; check
all of them before assuming "is there nothing else?"

| Doc | Holds |
|---|---|
| [TODO.md](TODO.md) (this file) | Open items not tied to a specific release or plan. The default place for new work. |
| [docs/V0.3.3.md](docs/V0.3.3.md) | Per-release polish list. Item-numbered, ✓ marks shipped. When v0.3.3 cuts, create `docs/V0.4.0.md` for the next cycle. |
| [docs/PLAN-MULTI-STORAGE.md](docs/PLAN-MULTI-STORAGE.md) | Multi-storage feature plan. The §Sequence section is its TODO. |
| [docs/PLAN-NFS-READ.md](docs/PLAN-NFS-READ.md) | JUKEBOX-on-threshold + async prefetch plan. Second-phase fix for the NFSv3 READ stall identified 2026-05-16; ships after the Spotlight-block fix. |
| [docs/PLAN-MULTI-DEVICE.md](docs/PLAN-MULTI-DEVICE.md) | Multi-device feature plan. Same shape — §Sequence enumerates remaining steps. |
| [docs/MISTAKES.md](docs/MISTAKES.md) | Numbered failure receipts. Entries marked "investigation pending" are implicit TODOs. |
| [docs/DECISIONS.md](docs/DECISIONS.md) | Dated decision journal. Each entry's "Verification plan" line is a forward-looking item. |
| [docs/PRE-LAUNCH.md](docs/PRE-LAUNCH.md) | Launch checklist for the public announcement. |
| [~/Labs/TODO.md](../TODO.md) | Cross-project backlog (secrets handling, Forgejo migration, project renames). Different scope. |
| `correspondence/*/letter.md` | Letters end with "Recommendation for tomorrow" lists. **Ephemeral** — a snapshot of one writer's view, not authoritative. Don't promote from here without re-considering. |

**Code-level `TODO`/`FIXME` comments are pointer comments only** —
they reference this file or another doc, not unsynced items. If
you find yourself wanting to leave a TODO in code, write it here
or in the appropriate doc above instead.

---

## On-return pickups — sessions 2026-05-11, 2026-05-14, and 2026-05-16

Items prior sessions surfaced but couldn't close without hands.
See [correspondence/12-autonomous-afternoon-2026-05-11/letter.md](correspondence/12-autonomous-afternoon-2026-05-11/letter.md)
and [correspondence/13-end-of-day-2026-05-11/letter.md](correspondence/13-end-of-day-2026-05-11/letter.md)
for context.

- [x] **Clean up the stale NFS mount** at `/private/tmp/comprador`
      — cleared 2026-05-14 (`sudo diskutil unmount force`).
- [x] **Diagnostic verification of MISTAKES 1a** — closed
      2026-05-14 in commit `5a19a3ac`. 13/13 FSStat calls
      logged `path=[]`; option 2 (storage ID in file handle) is
      the required fix.
- [x] **End-to-end verification of V0.3.3 #1** — verified
      2026-05-14 in commit `5a19a3ac`. Phone-side mutations
      surface on next READDIR; NFS-client dirlist cache
      documented as separate concern.
- [x] **9 GiB Attenborough cgo-fix vmmap retake** — verified
      2026-05-14. 67 VM_ALLOCATE regions, 8.3 MB RSS post-9-GiB
      Mac→phone transfer. Fix is solid.
- [x] **Validate fileSync-hold against a fresh drag-drop**
      — closed 2026-05-16 by **reverting `0d1418ac`** in commit
      `9239dcd7`. Two drags from Finder via the Xperia XQ-BT52
      mount empirically falsified the UX premise:
      - 9 KB file: WRITE held 69 ms, dialog dismissed honestly,
        mechanism verified.
      - 9 GB Attenborough.mkv: MTP SendFile ran for 7 min 7 s
        (~21 MB/s), all bytes verified on phone, but macOS NFS
        client surfaced "Server connections interrupted" at T+20 s
        and Finder showed no progress dialog for the remaining
        ~6 min 47 s.
      Any file whose MTP send exceeds macOS's NFS RPC timeout
      (~20–30 s, ~600 MB at 21 MB/s) trades the old early-dismiss
      lie for a scary-alert + no-dialog regression. See `9239dcd7`
      commit message for the full timestamps and analysis.
- [ ] **Investigate the first-drag-after-mount silent stall**
      ([MISTAKES.md §3 entry 4](docs/MISTAKES.md), open). Two
      sessions on 2026-05-16 reproduced a ~5 minute kernel-side
      stall on the first Mac→phone drag after a fresh mount;
      Finder shows "Server connections interrupted" at T+~20 s,
      and the bridge log shows zero traffic during the stall.
      Reproduced on both `c84db8cc-dirty` (pre-revert) and
      `fb4135a8-dirty` (post-revert), so this is not caused by
      `0d1418ac`. Next session priority — investigation order:
      (1) build a v0.3.3-tagged binary and test for the same
      stall; (2) if pre-branch, packet-trace the stall window;
      (3) if branch-introduced, bisect substantive code commits
      (suspects: `54225165`, `1c402e86`, `5bfd2462`).
      Likely pre-launch blocker for v0.4.0 if it reproduces in
      any user-visible scenario after a fresh Comprador start.
      Bridge log preserved at `build/dev-nfs-2026-05-16.log`.
- [ ] **Deliberate on FUSE-T as the next architectural pivot**
      (next session, post-fileSync-hold revert). The
      `ux_unavoidable_wait.md` memory note named FUSE-T as the
      only architectural escape for honest progress UX during
      slow backend writes; the 2026-05-16 fileSync-hold attempt
      bumped into the same wall from a different angle and
      confirmed the diagnosis. Decision points for the
      deliberation:
      - **Acceptance of an install dependency.** FUSE-T is
        third-party MIT-licensed; current ship is a single
        notarized .dmg with no prerequisites. Trade single-binary
        simplicity for honest progress UX.
      - **First-install friction.** System Extension approval
        flow on first run (~20 s of "approve in System Settings
        → Privacy & Security"). Welcome-window onboarding copy
        needs to absorb this.
      - **Scope of the migration.** MTP session goroutine and
        ObjectMap stay; NFS server + go-nfs vendor + helper
        plumbing all go. ~1 week of careful work + re-testing
        the multi-storage and AppleDouble cases we paid down on
        the NFS side.
      - **Security upside.** Drops the load-bearing invariant #1
        (127.0.0.1 NFS listener) from CLAUDE.md §Security
        Invariants; the bridge becomes a FUSE daemon with no
        listening socket.
      - **Sequencing relative to v0.4.0.** Letter 13 advised
        execution-not-investigation for the v0.4.0 push. The
        FUSE-T move is post-v0.4.0 by default; deliberate
        whether the progress-dialog problem is severe enough to
        block the launch instead.
- [ ] **Decide PR shape on `claude/multi-storage`.** Now 31
      commits ahead of master (after `9239dcd7`). Each commit is
      independently reviewable; letters 12 and 13 plus the
      `9239dcd7` revert receipt have the chronological summary.
      Push and merge at the architect's pace.

---

## Next concrete code work

- [ ] **Multi-device step 4 — bridge `--device-loc-id` CLI flag.**
      Per
      [PLAN-MULTI-DEVICE.md §6](docs/PLAN-MULTI-DEVICE.md) option A.
      Add the flag in `bridge/main.go`; teach
      `bridge/mtp/binding.go`'s `DetectDevice` to filter libmtp's
      raw device list to a single matching Location ID rather
      than picking the first MTP device on the bus. Swift side:
      `BridgeProcess.start` already receives `seizeForVendor` /
      `seizeForProduct`; thread `locationID` through similarly
      and pass it to the bridge. ~30 lines total, but verification
      needs two phones plugged in simultaneously to confirm each
      bridge claims the right one. After this lands, step 5
      (per-device DeviceWatcher wiring) and step 6 (menu UX)
      become unblocked.

---

## Pre-launch UX items (block v0.4.0 tag)

Items the launch playbook ([LAUNCH-PLAYBOOK-DRAFT.md](docs/LAUNCH-PLAYBOOK-DRAFT.md))
assumes have shipped before Day 0. Block the v0.4.0 tag.

- [ ] **Finder copy-progress regression — progress window tracks
      Mac→NFS-cache, not Mac→phone.** Observed 2026-05-18 evening
      during the step3 yield-test setup, against build `74702901`.
      Finder reports a Mac→phone copy as "done" when the bytes have
      landed in the bridge's local staging dir, but the libmtp send
      to the phone is still in flight. User sees "100%" while the
      file is not yet on the device.

      Architect characterised this as a *regression* — earlier
      Comprador versions reportedly tracked the MTP-send completion
      accurately. Bisect candidate: the resumable-upload commit-
      decoupling work in `bridge/webdav/resume_endpoint.go` and the
      NFS COMMIT path in `bridge/nfs/write.go`. The hypothesis is
      that COMMIT now returns when staging is complete rather than
      when libmtp confirms — investigate before v0.4.0 because the
      UX dishonesty (user thinks the file is on the phone, ejects,
      and it isn't) is a launch-grade trust problem.

      Not Step-3-introduced (Step 3 only touched READ-side cache
      code; the WRITE/COMMIT path is unchanged). Reported here so
      the next investigation has a durable record.

- [ ] **User-facing disclosure of the `ptpcamerad` kill.** Comprador's
      bridge kills `ptpcamerad` (and `AMPDeviceDiscoveryAgent`) to win
      the USB interface claim from macOS's photo-import broker (see
      [MISTAKES.md](docs/MISTAKES.md) entries 11, 19 and the seizure
      work in [DECISIONS.md](docs/DECISIONS.md)). User-visible
      consequence: while Comprador is running, other apps that read
      USB cameras and PTP/MTP devices — Image Capture, Photos
      auto-import, third-party photo importers — temporarily lose
      access. They recover when Comprador releases the device. This
      currently has no user-facing disclosure. Surface in three
      places:

      - **Welcome window** (`MenuBarApp/Sources/WelcomeWindow.swift`)
        — a single bullet in the "what to expect" copy. Friendly
        register; aim for the phrasing *"While Comprador is running,
        Image Capture and Photos auto-import pause for USB cameras.
        They resume automatically when you eject your phone."*
      - **Website FAQ**
        ([docs/WEBSITE-v0.4.0-DRAFT.md](docs/WEBSITE-v0.4.0-DRAFT.md))
        — a longer FAQ entry explaining the why (macOS's PTP
        coordinator is single-claim) for users curious about the
        underlying behavior. Anchor on the symptom, not the
        mechanism: "if Image Capture seems to stop seeing your
        camera while Comprador is mounted, that's expected."
      - **README's "What works"** section — one paragraph for
        technical readers and blog reviewers writing about the
        project. Honest disclosure beats discovery-by-bug-report.

      The pattern surveyed in [docs/COLLEAGUE-COPY-DRAFT.md](docs/COLLEAGUE-COPY-DRAFT.md)
      confirms competitors implying "fully automatic, no friction"
      and avoiding this disclosure (MacDroid's hero in particular).
      Our advantage in surfacing it: the disclosure is small, the
      friction is small, and naming it up front means no support
      tickets later asking why Image Capture stopped working.

- [ ] **Update detector with Homebrew-aware suppression.** The
      launch playbook commits to two distribution channels in
      parallel: direct .dmg from GitHub Releases (the canonical
      path for non-technical users via the website) and Homebrew
      Cask (for technical-adjacent users and a credibility marker).
      Each channel needs a different update story.

      - Direct-DMG users need an in-app update mechanism. Industry
        standard is **Sparkle** (`https://sparkle-project.org/`),
        which works with a signed appcast.xml hosted alongside the
        .dmg artifacts. Sparkle handles signature verification, EdDSA
        keys, delta updates, the in-app prompt UI, and the relaunch
        sequence. Mature, well-maintained, the obvious choice.
      - Homebrew Cask users have `brew upgrade --cask comprador` as
        their update path. An in-app Sparkle prompt for these users
        bypasses the package manager they explicitly opted into and
        breaks the reproducibility they care about. Sparkle must be
        suppressed.

      Detection of a Homebrew Cask install can use any of:

      1. **Path check.** Homebrew Cask on Apple Silicon installs to
         `/opt/homebrew/Caskroom/comprador/<version>/` and symlinks
         (or copies) into `/Applications/`. On Intel: `/usr/local/
         Caskroom/`. Check whether the running binary's
         `Bundle.main.bundlePath` resolves under either Caskroom
         prefix, or check whether either prefix contains a
         `comprador/<version>` directory.
      2. **xattr check.** Homebrew sets distinctive extended
         attributes on Cask-installed artifacts. Less stable than
         the path check; Homebrew has changed the metadata format
         before. Use as a secondary signal, not primary.
      3. **`brew list --cask` shell-out.** The most robust check,
         but requires shelling out and depends on `brew` being on
         PATH at runtime. Use as a last-resort verification.

      Recommended behavior when Homebrew is detected:

      - Suppress the Sparkle update prompt entirely.
      - Optionally, when Sparkle's internal version check detects a
        new release available, log a one-line hint to console
        (`NSLog`) pointing at `brew upgrade --cask comprador` so
        the technical user who tails the log sees it. No UI.
      - Never block app functionality on update availability.

      File touchpoints: new `MenuBarApp/Sources/UpdateChecker.swift`
      wiring Sparkle. The Homebrew detection probably belongs in a
      small `InstallSource.swift` so future code can ask "where did
      this binary come from?" cleanly.

- [ ] **Hold NFS WRITE response until MTP commit completes.**
      Symptom (re-surfaced 2026-05-14): architect drag-and-dropped a
      9.094 GB file (`David.Attenborough…mkv`) into the mount via
      Finder. Finder's progress bar reached 100% and dismissed in
      under a minute — the NFS WRITE RPCs to the bridge complete at
      memory speed. The architect read this as "copy complete," tried
      to access the file, found it incomplete, and reported a *silent
      regression*. The transfer was actually fine — the bridge was
      mid-way through the synchronous MTP SendFile, which is
      USB-bandwidth-bound at ~22 MB/s and takes ~7 minutes for 9 GB.
      Bridge log was clean, no errors, file landed byte-perfect on
      the phone. The bug is the **progress-bar lie**: Finder reports
      done when bytes arrive at the bridge, not when bytes arrive at
      the phone.

      **Fix: hold the NFS WRITE FILE_SYNC response until the MTP
      SendFile actually commits.** The final `WRITE how=2` RPC (the
      sync commit, currently logged just before MTP SendFile kicks
      off) should not return success until `LIBMTP_Send_File_From_Handler`
      has returned cleanly. Finder will then keep the progress bar up
      for the real duration of the transfer. The apparent copy time
      goes from ~30s to ~7min for a 9 GB file, which is honest — that
      IS how long writing 9 GB over USB-MTP takes.

      Trade-off: the NFS client will see writes that take much longer
      to acknowledge than expected. Most clients tolerate this fine
      (their write loop just blocks longer on the final flush); risk
      is a heuristic NFS-client timeout firing on multi-minute syncs.
      macOS default NFS retransmit timeout is generous (60s base with
      exponential backoff, ~10min ceiling under default mount opts);
      the bridge should survive but should be tested against the
      9 GiB workload before shipping. If timeouts do fire, the
      fallback is to ack the FILE_SYNC immediately but defer reporting
      "size" via subsequent stats — coarser but no client-side risk.

      File touchpoints:
      - `bridge/nfs/write.go` — block the FILE_SYNC commit path on
        the MTP send completion future. Currently the idle-flush
        pattern kicks the MTP send asynchronously; the WRITE
        completes immediately and the architect's "silent failure"
        is born. The trade is between a fast-but-misleading progress
        bar and an honest-but-longer-feeling one. The latter is the
        correct choice for a tool whose value prop is *honesty about
        what's actually happening*.
      - Consider: keep async idle-flush for *small* files (where
        the discrepancy is sub-second and the user benefit of a fast
        progress bar exceeds the misleading-progress cost), and
        synchronous commit for *large* files (where the discrepancy
        is minutes and the misleading progress bar caused a real
        false-regression report from the architect). Threshold
        suggestion: 100 MB.

      Acceptance: drag a 9 GB file via Finder; the Finder progress
      bar stays up for the full ~7-minute MTP commit duration; bytes
      written to phone advance monotonically alongside the visible
      progress; final commit lands byte-perfect and progress
      dismisses simultaneously.

      Captured 2026-05-14 from architect's drag-drop test on the
      Xperia during the verification sweep. This was the trigger for
      "hard stop, regression detected"; the diagnosis was that the
      reported failure mode is in fact the progress-accuracy bug
      we've been carrying as known UX debt. Promoted from
      acknowledge-someday to v0.4.0-blocker.

- [ ] **Respectful Defaults pass.** Bundle of small UX items that
      together codify Comprador as a *respectful utility* — the
      brand positioning the launch playbook is built on. Each item
      is five-to-twenty lines of Swift; the value is in shipping
      them as a coherent set, not piecemeal. The colleague-copy
      survey ([docs/COLLEAGUE-COPY-DRAFT.md](docs/COLLEAGUE-COPY-DRAFT.md))
      confirmed competitors imply *fully-automatic, no-friction*
      and stop there; Comprador's wedge is doing this work that
      they skip.

      Verify or implement (each):
      - **Menu-bar-only** (`LSUIElement=true` in Info.plist; no dock
        icon). Likely already in place; confirm.
      - **Tooltip on the menu bar icon** showing current state —
        *"No device connected"* / *"Pixel 6 mounted"* / *"Connecting…"*.
        State visible without a click.
      - **Reduced Motion respect.** The connecting-state pulse
        animation in `AppDelegate.startPulse` should check
        `NSWorkspace.shared.accessibilityDisplayShouldReduceMotion`
        and skip the animation when true. Static-icon connecting
        state is acceptable; the pulse is a flourish, not load-bearing.
      - **Sleep / wake handling.** Subscribe to
        `NSWorkspace.willSleepNotification` (unmount + tear down +
        release USB claim) and `didWakeNotification` (re-detect, reconnect
        if phone still attached). Without this, sleep-with-phone-
        mounted produces broken state on wake — Image Capture and
        others recover; Comprador silently doesn't.
      - **Don't prevent system sleep.** `IOPMAssertionCreateWithName`
        with `kIOPMAssertionTypeNoIdleSleep` ONLY while a transfer is
        actively in flight (NFS WRITE RPCs queued or staging file
        non-empty). Release immediately on idle. Many indie utilities
        forget this and quietly burn user battery.
      - **Welcome window once-only enforcement.** First launch only;
        not on update, not on relaunch. The `Comprador.didShowWelcome`
        flag exists; confirm no future feature accidentally re-shows it.
      - **System notifications sparingly.** Only post
        `UNUserNotification` for events the user must act on
        (currently: *"phone connected, choose File Transfer on its
        screen"*). Audit existing notification sites; remove any
        *"successfully mounted"* / *"ejected"* / *"transfer complete"*
        prompts. Notification fatigue is real and recovery from a
        notification turn-off is hard.
      - **Clean uninstall.** Drag-to-trash should leave zero
        artifacts. With v0.4.0's helper retirement this gets dramatically
        easier — no LaunchDaemon to register, no `/etc/hosts` block
        to unwind. Verify: drag to trash, restart, look for orphaned
        plists / preferences / launchd registrations. Mention in
        website FAQ as a feature.
      - **No dock bounce, ever.** `NSApp.requestUserAttention` is a
        sealed entry point; codify in a comment that no caller may
        invoke it.
      - **Codify: no telemetry, no phone-home.** Already a security
        invariant ([docs/SECURITY.md](docs/SECURITY.md)); state as a
        *UX promise* on the website. Users notice the assertion even
        if they don't verify the code.

- [ ] **Donation infrastructure — legitimate nudges, no dark patterns.**
      The project's pitch is respect-by-default; donation flow has to
      match. The launch playbook
      ([docs/LAUNCH-PLAYBOOK-DRAFT.md](docs/LAUNCH-PLAYBOOK-DRAFT.md))
      sketches the strategic frame. Concrete items:

      - **GitHub Sponsors button** on the repository. Five-minute
        setup; visible on every repo visit. Strictly additive to the
        existing Interac e-Transfer path in README.md.
      - **Quiet *"Support Comprador…"* menu item** in the menu bar
        dropdown. Never bolded, never tagged *"new!"*. Present, not
        pushy. Opens the Odometer window (item below) or links
        directly to the donation page — see the Odometer entry for
        rationale on routing through it.
      - **"Where the money goes" page** on the website. Concrete
        numbers: Apple Developer Program $99/year, domain cost,
        codesigning costs. Honest transparency activates trust where
        vague *"support our work"* activates suspicion.
      - **README Support section moved above License**, not buried
        in the footer below the third-party-notices link. Visible to
        anyone reading down the page; not intruding on the install
        path.
      - **Donor count, opt-in** (if GitHub Sponsors). Quiet visible
        *"N supporters this month"* on the repo's README. Social proof
        that's true and verifiable.
      - **Annual transparency post**, once per year. *"Comprador cost
        $X to run, received $Y in donations, here's what next year
        looks like."* Doesn't ask; informs. Builds long-term trust
        with the user base that cares.

      Dark patterns to refuse, codified here so a future contributor
      doesn't propose them in good faith:

      - No modal donation dialogs interrupting the connect flow.
      - No time-pressure copy (*"Only N days left to…"*).
      - No confirm-shaming dismiss buttons.
      - No first-launch donation prompt.
      - No pop-up frequency tricks (showing every N launches).
      - No pre-checked donation checkboxes anywhere.
      - No roach-motel recurring donation flows; cancellation as easy
        as starting.
      - No fabricated testimonials or inflated stats.

- [ ] **Odometer window** — user-initiated usage stats with quiet
      donation routing. New menu bar item (*"Odometer…"*) opens a
      modest window showing the user their own Comprador mileage.
      Inverts the donation flow from app-pushes-ask to
      user-pulls-info-sees-own-value-encounters-support-option.

      Stats to show (capped — do not add more without re-discussing):
      - Total bytes transferred (in and out, separated for honesty)
      - Total files transferred
      - Devices ever mounted (count; optionally names, opt-in to show)
      - First-use date
      - Last mount date
      - Cumulative mount-time (hours)

      Stats to deliberately NOT show:
      - Per-device transfer breakdowns (creates a privacy-share hazard
        if a user screenshots)
      - File-type histograms (same)
      - Time-of-day patterns (creepy)

      UI:
      - Window opens from menu bar dropdown via the *"Odometer…"*
        item (single click, no submenu nesting)
      - Stats laid out simply — labels + numbers — no charts or
        graphs in v0.4.0
      - Footer button: *"Support Comprador"* (single button, neutral
        verb). Opens donation page in the user's default browser.
      - Footnote near the bottom of the window: *"All values stored
        locally on this Mac. Nothing leaves your device."* Codifies
        the no-telemetry promise in the place a user might wonder.
      - *"Reset counters"* link (small, low-contrast, near the
        footnote). Privacy gesture for users who want a fresh slate.

      Storage: a small SQLite file or plist in the app's container
      (Application Support / com.comprador.app/). Increment on each
      mount, each completed transfer, each device first-seen. Never
      transmitted.

      Naming: *"Odometer"* (deliberate — on-brand for the
      mechanical-historical comprador-as-ledger-keeper register).
      Not *"Activity,"* not *"Usage,"* not *"Statistics."*

      Feature-creep risk to monitor: Odometer → achievement
      badges → leaderboards → gamification. Each step looks small
      from the previous. Discipline: ship the modest version, refuse
      additions. The Odometer is honest mileage, not a dashboard.

- [ ] **Website CSS — Apple-clean composition with period flavor.**
      The body copy in
      [docs/WEBSITE-v0.4.0-DRAFT.md](docs/WEBSITE-v0.4.0-DRAFT.md)
      and the gh-pages preview at
      <https://terraceonhigh.github.io/Comprador/> currently render
      via `build/render-website.py`'s clinical-modern template. That
      template fights the comprador frame — the body copy honours the
      metaphor structurally but the typography and colour palette
      don't. The CSS layer needs a pass that takes the Merian/Sluyter
      pomegranate icon as seriously as the icon already takes itself:

      - Cream paper background (not pure white)
      - Deep crimson — seal-wax / merchant-chop register — as the
        single accent. The bloom in the existing logo plate is
        already roughly this red; color-pick from there.
      - Transitional serif typography (Caslon, Baskerville, EB
        Garamond, Source Serif — anything that belongs to the same
        century as the engraving)
      - Ornamental hairline rules between sections (hairline +
        small ornament + hairline, not the modern flat HR)
      - Period-marginalia treatment for the etymology / fine-print
        ("Apple Silicon, macOS 13 or later. No iPhone support.")
      - Bordered-engraving frame around the hero image, period
        convention rather than modern edge-to-edge
      - The sign-off (`Comprador. At your service.`) gets small caps
        + the seal-red, sitting on a hairline ornament — the page's
        single explicit period inscription, in keeping with the
        Apple-with-period-flavor distribution we agreed
      - Body copy stays Apple-clean throughout; the CSS does the
        period work without the prose committing to the register

      **Blocker: needs the architect to spend a few hours on
      Pinterest** (or equivalent moodboard) gathering visual
      references they actually like — period commercial broadsides,
      18th-century natural-history engravings, modern reissues of
      the same (the Merian/Sluyter plate's own milieu), Wes
      Anderson title cards if relevant, museum-collection
      letterpress samples. Without that reference set, any CSS pass
      is guessing at what *feels right* and risks landing somewhere
      twee or LARP-y. Once the moodboard exists, the CSS is a
      bounded afternoon of work — typeface licensing decisions,
      colour values, hairline-ornament SVG sourcing, then layout.

      Risk to flag once unblocked: period treatment done halfway
      reads as costume. The discipline mirrors the Odometer's: commit
      fully to the period register in the visual layer, or stay
      clinical-modern. The middle is the bad place.

      Cross-references:
      [docs/COLLEAGUE-COPY-DRAFT.md](docs/COLLEAGUE-COPY-DRAFT.md)
      (positioning context),
      [docs/APPLE-COPY-CONVENTIONS-DRAFT.md](docs/APPLE-COPY-CONVENTIONS-DRAFT.md)
      (the body-copy register the visual is supposed to support),
      and the gh-pages preview as the current baseline.

- [ ] **Documentation sweep before v0.4.0 primetime.** The README
      is the first surface most GitHub-arriving users see; secondary
      docs (USER.md, PRE-LAUNCH.md, ARCHITECTURE.md, FAQ entries
      across multiple files) are where curious users dig in next.
      All of these were touched in the 2026-05-11 staleness audit
      (commit `ccf324fe`) which fixed the WebDAV-still-mentioned
      bugs but did not rewrite for marketing or post-v0.4.0 feature
      set. A careful pre-tag pass is needed.

      Items to address in the README specifically:

      - **Headline and tagline** — confirm they still read right
        after the marketing arc (composite copy + Apple-conventions
        + colleague survey + Soduto footer + SEO subhead all settled
        today, but only on the website draft; the README hasn't
        been updated to match).
      - **"Known issues" section** — multi-device shipped (drop the
        *"Single device at a time (today)"* line), AppleDouble
        filter shipped, TTL refresh shipped. The "first-plug-after-
        app-start may fail" section needs honest update against
        whatever v0.4.0 actually does about it.
      - **Support section moved above License** per the donation
        infrastructure TODO.
      - **"What works" enumerates v0.4.0 features** — per-storage
        quota, phone-side change reflection, AppleDouble filtering,
        concurrent multi-device, helper retirement.
      - **FAQ stale-claim sweep** — especially anything mentioning
        WebDAV, the helper, or the privileged-mount path that's
        gone.
      - **Code-fenced commands** still resolve correctly
        (`make app-swiftc`, `make test-md5`, `make bridge-test`,
        the new `make test-e2e` if it lands).
      - **Download / release-link URLs** point at the v0.4.0
        release page once it exists.

      Other documentation surfaces that should sweep in the same
      pass:

      - **USER.md** — does the user model still match the actual
        v0.4.0 user (now that some "future" items are present-tense)?
      - **PRE-LAUNCH.md** — go/no-go items, what's disclosed, what's
        deferred. Several items shipped today; the checklist needs
        a pass.
      - **ARCHITECTURE.md** — WebDAV retirement may have left stale
        references; the per-device subprocess shape of multi-device
        wasn't documented when this was written.
      - **DECISIONS.md** — confirm no decisions were invalidated by
        v0.4.0's shape that aren't already noted.
      - **MISTAKES.md** — entries whose underlying code retires
        with v0.4.0 (WebDAV-section already tagged with the
        sign-of-life header; helper-section similarly). Spot-check.
      - **NOTICES.md** — third-party-licence accounting, especially
        if any vendored dependency moved.

      Budget: half a day of careful reading + writing. The v0.4.0
      ship blocks on this not being stale. A blog post / Show HN
      / Mac-press article from the launch playbook will quote from
      whatever's at the top of the README; getting the wording
      right *here* is the marketing channel that matters most.

---

## Verification follow-ups

Carried forward from DECISIONS.md, not blocking but real:

- [ ] **9 GiB Attenborough.mkv vmmap retake on the cgo-fix
      bridge.** [DECISIONS.md "Vanquishing the per-callback
      VM_ALLOCATE leak"](docs/DECISIONS.md) lists this as the
      proper acceptance criterion (physical footprint < 1 GB,
      VM_ALLOCATE regions < ~50 vs the pre-fix 409). Today's
      partial confirmation was 49.6 MB transfer → 8.4 MB RSS —
      directional evidence but not the spec'd test.

---

## Post-v0.4.0 backlog — durable corpus stewardship

Forward-looking items that wait until v0.4.0 has shipped and the
project is a stable reference point an external essay could
plausibly cite.

- [ ] **Externalize the methodological corpus.** Raised during the
      2026-05-11 evening reflection on whether `correspondence/`
      and the meta-docs have value beyond the project. Four
      candidate threads identified, each is a real piece of writing,
      pick one and do well rather than try to harvest all at once:

      1. **AI-pair-programming methodology essay** drawing on the
         `correspondence/` archive. The thinnest existing supply in
         the public discourse; most published material on
         working-with-AI is hype, demos, or manifestos rather than
         working examples over real time. Comprador has the rare
         primary-source material — a month of disciplined
         architect-to-agent correspondence on a real product, with
         methodological lessons captured *in the moment they were
         learned* rather than retrospectively.
      2. **Three to five distilled methodological essays** on the
         generalizable lessons: *run the syscall before designing a
         privileged helper*, *check the scope of the evidence not
         just its quality*, *a wrong claim that prompts the right
         investigation*. These work whether the collaborator is a
         Claude, a junior engineer, or oneself six months ago.
         Lower distinctiveness but durable.
      3. **Focused macOS-internals posts** on the specific findings
         the project produced — the webdavfs writeseq cap, the
         ptpcamerad userspace-broker realization, the
         ImageCaptureCore PTP-mode scope ceiling, the BTM-corruption
         arc. Small audience (working indie Mac developers) but the
         *right* audience; Apple's official docs will never carry
         these findings.
      4. **A "how this project documents itself" pattern-paper** —
         the decision-journal + numbered mistakes log + plan docs
         + correspondence-style retrospectives as a working model
         for low-staffed open-source projects with high quality
         bars. Likely landing point: a single Hacker News post.

      **Right time to publish: post-v0.4.0 ship.** Until then, the
      material is in the right place — captured in-repo, available
      for reference, not yet curated for an external audience. The
      *"we shipped it, here's what it taught us"* framing earns
      much more attention than the *"we're shipping it eventually"*
      framing.

      **Blocked on:** confirmation that humboldt-side backup is live
      and `correspondence/` is captured. The corpus itself is what
      makes the harvest possible; protecting it before any external
      publication is non-negotiable, because publishing creates
      both the value-of-survival incentive *and* (modestly) the
      risk-of-loss attention. The calling card to humboldt's
      Claude is at
      [bazzite-server-plan/Correspondance/12-from-comprador-on-backup.md](../bazzite-server-plan/Correspondance/12-from-comprador-on-backup.md).
      Once backup confirms, this item unblocks.

---

## Tidying

Discussed 2026-05-11; deliberately deferred to a focused tidying
session rather than rolled into a code commit. Organized by
reversibility — Tier 1 is clearly safe, Tier 3 is real
architectural cleanup.

### Tier 1 — safe deletions

- [ ] **Delete `bridge/cmd/ictest1/` and `bridge/cmd/ictest2/`**
      plus their Makefile targets (`ictest1`, `ictest2`) and the
      `ICTEST1_OUT` / `ICTEST2_OUT` variables.
      [DECISIONS.md "ImageCaptureCore investigation"](docs/DECISIONS.md)
      explicitly marks these "deletable in a single commit once
      the receipt in RESEARCH-IMAGECAPTURECORE.md is sufficient on
      its own" — and it is. Net: ~350 lines of Swift gone, no
      information loss.
- [ ] **Delete `build/dir-diff.py` and `build/list-phone.py`.**
      Ad-hoc Python scripts from the 2026-05-11 directory-copy
      investigation, superseded by `test-md5.sh` as the canonical
      verification tool. Already gitignored (`build/` is
      gitignored), so this is working-directory hygiene only —
      `rm` and move on.

### Tier 2 — reasonable, worth a moment first

- [x] **`docs/V0.4.0-DRAFT.md` drafted 2026-05-11.** Collects
      the v0.4.0 retirement items into one place, mirroring
      V0.3.3.md format. Architect to review and promote to
      `V0.4.0.md` (drop the DRAFT markers in filename and inline
      header) when v0.4.0 work picks up. Five items in the
      draft: WebDAV retirement, helper retirement, the
      system-extension entitlement decision, V0.3.3 #2 closure
      contingent on the helper-retirement choice, and the cgo
      acceptance-test retake.
- [x] **Trim the "Original spec preserved for reference" tails**
      in shipped items. 2026-05-11: V0.3.3.md item #1 trimmed
      (~30 lines) and TODO.md "Phone-side checksum verification"
      trimmed (~30 lines). Items #4 and #5 didn't actually grow
      tails — earlier framing overcounted.

### Tier 3 — real architectural cleanup (bigger, defer to a focused session)

These are substantial PRs, not "tidying." Filed here so they're
not forgotten when V0.4.0 is in flight.

- [ ] **Retire the WebDAV mount path entirely.** NFS has been
      the default since v0.3.0 (2026-05-09) and is verified
      working for the architect's daily use. The WebDAV
      apparatus is dead code: `bridge/webdav/` package, the
      `MountManager.mount` (vs `mountNFS`) WebDAV branch in
      `MenuBarApp/Sources/MountManager.swift`,
      `ResumeCompanion` and its companion port, the writeseq-
      cap heuristics, the bridge-side resume endpoint, the
      WebDAV-specific code paths in BridgeProcess. Likely
      ~1/3 of the codebase. Removing it shrinks the bundle,
      simplifies the connect flow, and eliminates the
      ~90s mount-time hint copy from the menu.
- [ ] **Retire the privileged helper.** Per SECURITY.md, the
      single largest privilege-escalation surface in the
      bundle. NFS doesn't need it for the mount; the only
      remaining use is the optional cosmetic `.local`
      hostname rewrite (`MenuBarApp/Sources/HelperClient.swift`).
      With WebDAV retired (above), the only thing the helper
      still does is /etc/hosts editing for the
      Pixel-6.local → Pixel-6 cosmetic. Decide: (a) drop the
      feature entirely and live with `.local` (V0.3.3 #2's
      option C); (b) drop the helper, accept the cosmetic
      via a one-time root prompt at install (option B of #2);
      (c) keep the helper as a tiny single-purpose daemon.
      Decision blocks the deletion of `helper/`,
      `MenuBarApp/Sources/HelperClient.swift`, the
      `BUNDLE_HELPER` Makefile recipe, the LaunchDaemon
      plist, and the SMAppService.daemon registration.

---

## ✓ Closed — cgo callback buffer reuse

Shipped 2026-05-06 in commit `90fb7216` ("mtp: reuse one buffer
per session in cgo callbacks"). Multi-device's hard prerequisite
is met; the work in [PLAN-MULTI-DEVICE.md](docs/PLAN-MULTI-DEVICE.md)
is unblocked. Implementation is in
[bridge/mtp/binding_callbacks.go](bridge/mtp/binding_callbacks.go)
(reuses `entry.buf` from the registry instead of `make([]byte, ...)`
per callback) and [bridge/mtp/binding.go](bridge/mtp/binding.go)
(`readerEntry` / `writerEntry` hold the buffer alongside the
io.Reader/io.Writer). Receipt and the alternatives considered are
in [DECISIONS.md "Vanquishing the per-callback VM_ALLOCATE
leak"](docs/DECISIONS.md).

Empirical confirmation is the next missing piece — the original
9 GiB Attenborough vmmap reading should be re-taken on the fixed
bridge to verify physical footprint stays under 1 GB. Not done
yet; not blocking.

---

## Architecture risk — partially mitigated, still open

- [ ] **Reconsider the architecture if RAM is a binding constraint.**

      **Original observation 2026-05-06 (pre-streaming-refactor):** on
      an 8 GiB-RAM Mac with ~67 MiB free, dragging a 9.09 GiB
      Attenborough.mkv via Finder: Finder's progress bar climbed
      silently while webdavfs accepted bytes it had nowhere to buffer;
      the bridge received zero body bytes for ~3 minutes; eventually
      webdavfs flushed ~4.3 GiB in a burst (truncated by the writeseq
      cap). Phase 2 auto-completion succeeded but the *journey* —
      opaque memory starvation, intermittent EADDRNOTAVAIL on the
      companion's URLSession, false-timeout in URLSession during the
      MTP commit window — was unacceptable for non-developer users.

      **What changed (commit 0c5a18e):** the bridge no longer buffers
      PUT bodies in a `bytes.Buffer`; it streams every Write directly
      to a staging file on disk, and opens that staging file with
      `F_NOCACHE` when reading it back for the MTP send. Go-side heap
      for the WebDAV layer dropped from "approximately the file size"
      to a handful of MiB regardless of upload size. webdavfs's
      writeseq cap stayed comfortably high on a fresh-boot 8 GiB Mac;
      the same 9.09 GiB Attenborough.mkv that previously hit Mode A
      went through in a single uninterrupted PUT.

      **What we then learned (re-verification 2026-05-06):** during
      the MTP `SendFile` *after* the PUT body has fully arrived, the
      bridge process still hits ~10 GB physical footprint. The hog
      isn't the page cache (F_NOCACHE worked) and isn't the WebDAV
      buffer (streaming worked) — it's the **cgo callback path**:
      `goDataGetFunc` (and `goDataPutFunc` on the GET side) calls
      `make([]byte, int(wantlen))` per invocation, generating roughly
      one Go heap allocation per libmtp chunk (~22 MiB each, ~400
      for a 9 GiB file). macOS's `MADV_FREE` policy keeps those
      allocations in the process's address space until kernel
      reclaim. Vmmap shows them as 409 `VM_ALLOCATE` regions. See
      MISTAKES.md entry 8a — the fix is reusing one buffer per
      session via the registry, ~30 lines of code, listed in High
      impact below.

      **Until that fix lands:** Mode A on 8 GiB Macs is no longer
      common because webdavfs now stays well-fed during the PUT;
      but every multi-GiB transfer still leaks ~9 GiB of process
      memory into swap until the bridge dies. The system thrashes
      but doesn't crash. **Don't click multi-GiB phone files in
      Finder while a transfer is recent** — see MISTAKES.md
      entry 11d-tris for why QuickLook is an attractive nuisance.

      **What's still open even after the cgo fix:** files larger
      than the headroom we'd then have (probably "most of available
      RAM") will still hit the writeseq cap. Phase 2 catches that
      case automatically, but the user-visible journey reverts to
      the pre-mitigation pattern: a -36 dialog while the companion
      silently completes the upload over ~7 minutes. That's the
      remaining bad UX cliff.

      Architectural options for closing the cliff entirely, in
      decreasing order of preserving the current Finder-volume model:

      1. **Drop webdavfs from the upload path** (read-only mount for
         browsing; uploads via a Finder Service or sidebar entry that
         hands the source path directly to the bridge, bypassing
         chunked PUT entirely). Eliminates the writeseq cap. Hardest
         architecture change, biggest UX win for very large files.
      2. **Sidebar-only Finder integration** (no mounted volume at
         all) — like Image Capture. Loses "browse the phone from
         Finder" but eliminates webdavfs entirely.
      3. **File Provider extension** — Apple's first-party FS
         extension API. Sandboxed, memory-managed by the system, but
         USB device access from a File Provider extension is deeply
         restricted. Investigated and rejected in `CLAUDE.md` ("Why
         not File Provider API?"); worth re-examining only if (1) and
         (2) turn out unworkable.

      Pre-1.0 for non-developer users: probably no longer urgent
      after the streaming refactor — the 8 GiB Mac case is genuinely
      shippable now. But "files >12 GiB on low-RAM Macs go through
      Mode A with a -36 flash" is a known cliff worth deciding about
      before any user-visible 1.0 marketing makes claims about
      arbitrary file sizes.

## High impact (correctness / UX friction)

- [x] **cgo MTP callback: reuse buffer per session instead of
      allocating per call.** Shipped 2026-05-06 in `90fb7216`. See
      the closed roadmap-imperative section above and
      [DECISIONS.md "Vanquishing the per-callback VM_ALLOCATE
      leak"](docs/DECISIONS.md) for the rationale and alternatives
      considered. Verification by `vmmap` re-take on a multi-GiB
      transfer is the open follow-up.

- [ ] **Make GETs cancellable (revisit longstanding TODO).** Tied
      to MISTAKES.md entries 11d (deadlock under read pressure)
      and 11d-tris (QuickLook hazard). When a Finder click on a
      large phone file triggers a multi-minute MTP read, dismissing
      the QuickLook should kill the in-flight read, not let it run
      to completion blocking every other operation. Implementation
      hooks: pass a `context.Context` from the HTTP handler down
      through the session goroutine; on context cancel, call
      `LIBMTP_Cancel_Operation` (if libmtp supports it for the
      relevant transaction type) or close the device-side USB
      transfer to make libmtp's read return.

## High impact (UX friction)

- [x] Volume shows as "127.0.0.1" in Finder sidebar — should show device name (e.g. "Pixel 6")
      Bridge now registers `<DeviceName>.local → 127.0.0.1` via mDNS using
      `dns-sd -P` and advertises the URL with that hostname. NetFS auto-names
      the volume from the URL host, so Finder now sees `/Volumes/Pixel-6.local`.
      Verified end-to-end with a stub WebDAV server.
- [x] Drop the `.local` suffix from the Finder sidebar entry (Pixel-6.local → Pixel-6)
      A privileged helper (LaunchDaemon registered via `SMAppService.daemon`)
      now manages a block in `/etc/hosts` so the bridge can advertise
      `http://Pixel-6:port/`. macOS prompts the user once to approve it in
      Login Items; afterwards every device gets a clean single-label name.
      Strict server-side validation (`^[A-Za-z][A-Za-z0-9-]{0,62}$`, no
      reserved labels) prevents impersonation of real domains. mDNS remains
      as a fallback when the helper isn't approved.
- [x] Device name shows "Android Device" — fall back to `LIBMTP_Get_Modelname` when friendly name is empty (already in `binding.go`)
- [x] Login item registration — offer to start at login on first launch via `SMAppService`

## Security cleanup — v0.4.0 priority

- [ ] **Remove the privileged helper (`comprador-helper`,
      `SMAppService.daemon`).** Per
      [CHANGELOG v0.3.0](CHANGELOG.md), the helper is no longer
      invoked on the NFS mount path — it remains bundled only
      for legacy WebDAV cosmetic features (hostname rewriting
      via `/etc/hosts` to drop the `.local` suffix). It is the
      **single largest privilege-escalation surface in the
      bundle** ([SECURITY.md](docs/SECURITY.md)). Removal blocks
      on retiring the WebDAV mount path entirely; bump it from
      "slated for v0.4.0" to "the v0.4.0 priority cleanup item."
      Helper code in [helper/](helper/), bundled in
      `$(SWIFT_APP)/Contents/MacOS/comprador-helper` per the
      `BUNDLE_HELPER` Makefile recipe.

- [ ] **Subscribe to upstream libmtp / libusb releases.** No
      formal cadence today. Manual check per Comprador release
      cycle: hit
      [libmtp upstream](https://sourceforge.net/projects/libmtp/files/libmtp/)
      and
      [libusb releases](https://github.com/libusb/libusb/releases)
      before tagging each v0.x.0; bump if a security fix has
      shipped. Captured in [SECURITY.md "Tracked items"](docs/SECURITY.md).

## Morning review — human-facing docs

- [ ] **Walk through human-facing docs in the morning** with fresh
      eyes after last night's substantial changes. The day generated
      a lot of new doc surface (SECURITY.md, PRE-LAUNCH.md,
      RESEARCH-IMAGECAPTURECORE.md, PLAN-MULTI-STORAGE.md,
      PLAN-MULTI-DEVICE.md, USER.md updates, OPENMTP/SWIFTMTP-NOTES
      updates, NOTICES.md "Logo" section, CLAUDE.md "Security
      Invariants" section) and substantial restructuring (sibling
      repos moved to references/, icon pared to one PNG, multi-device
      commitment now in the affirmative). Update as necessary:
      - **README.md** — does the headline still read right? Does
        "Single device at a time (today)" still match where we are?
        Is the architecture section consistent with the bridge/icon
        paring?
      - **CHANGELOG.md** — anything we want to write before the
        next tag?
      - **The forensics docs (OPENMTP/SWIFTMTP/COPYPARTY/CRYPTOMATOR/
        GO-NFS/LIBNFS-GO NOTES.md)** — references/ paths verified;
        any stale claims now that the architectural picture has
        shifted?
      - **USER.md** — does the "What we are building toward" section
        still match what we're committing to?
      - **PRE-LAUNCH.md** — the launch checklist; sanity-check the
        hard/soft/nice-to-have buckets.

      A morning pass with coffee will catch the inconsistencies a
      tired late-evening eye missed.

## Medium impact (reliability)

- [ ] Error recovery — detect bridge crash mid-session, auto-restart
- [ ] Handle detach during file transfer gracefully (don't hang Finder)
- [x] **Reattach-during-unmount race leaves Comprador in dead state.**
      Reproduced 2026-05-06: after a successful mount, a USB
      detach+reattach storm fires within milliseconds (typically
      phone-side: screen sleep, MTP layer flutter). The reattach
      handler runs while the corresponding unmount is still in flight,
      sees `isMounted == true`, logs `Ignoring attach — already
      mounted`, and discards the event. Unmount then completes, the
      bridge is gone, no further attach event ever arrives, and the
      menu bar app is stuck in detached-no-bridge state until the user
      physically replugs. Two-line fix sketched in
      [docs/MISTAKES.md](docs/MISTAKES.md) entry 19a: track
      `pendingUnmount` and queue the reattach for after, OR
      synthesise an attach at the end of every successful unmount if
      IOKit still sees the device. Latter is cleaner — it's a one-shot
      "did the bus settle in a state that needs us?" check at unmount
      completion.
- [ ] Session recovery — reopen MTP session on corruption without full bridge restart
- [ ] **Make Finder error -36 disappear for very large files.**
      **Decision 2026-05-06**: pursuing option C (bridge ↔ Swift companion
      + direct source-file read on truncation). Options A and B were
      considered and rejected: A leaves the error visible as a documented
      limitation; B still surfaces a modal -36 dialog plus an implicit
      "drag again" rule, which is user-visible complexity by the standard
      we set. C is the only path that makes the failure invisible. Logging
      the choice for posterity — the future will judge it as either
      brilliance or hubris, depending on whether the source-discovery
      heuristic holds up. See [docs/RESUMABLE-UPLOADS.md](docs/RESUMABLE-UPLOADS.md)
      for the architecture.
      Apple WebDAVFS's writeseq path truncates chunked PUT bodies at a
      memory-pressure-dependent cap (observed at ~4 GiB on a fresh Mac
      2026-05-05; could be tens of MiB under load). PR #2's truncation
      guard prevents corrupt half-uploads — that's correct — but the user
      still sees -36 if their file is bigger than today's cap. Three
      options worth trying, in order of cost:
      1. ~~**`MNT_SYNCHRONOUS` mount flag**~~ — confirmed dead 2026-05-05.
         Set `kNetFSMountFlagsKey = MNT_SYNCHRONOUS` on the NetFS mount
         call; the kernel mount syscall accepts it but webdavfs's
         `webdav_mount` filters it out before it reaches the VFS state.
         `statfs()` on the mounted volume reads `f_flags = 0x1c`
         (NOEXEC+NOSUID+NODEV only), no SYNCHRONOUS bit. Don't retry.
      2. **Bridge-side persistent partial buffering with auto-merge on
         retry.** When the truncation guard fires, persist the partial
         body to disk keyed by `(path, X-Expected-Entity-Length)`. On
         the next PUT to the same path with the same expected size,
         skip bytes already cached and append the new ones; commit to
         MTP once total length is reached. UX: one -36 dialog, then a
         second drag completes the upload. Zero menu items, zero
         settings, zero education. Two days of work, low risk.
      3. **Bridge ↔ Swift XPC companion + direct source read on
         truncation** (deferred). On `REFUSING truncated upload`, the
         bridge signals the Comprador app, which uses NSMetadataQuery
         to locate the source file by filename + recent mtime, opens
         it directly bypassing webdavfs, and streams the missing tail
         to `/_comprador/resume`. UX: no -36 at all, file appears on
         phone. Risk: source-match is heuristic and can grab a
         like-named file instead. Defer until #1 and #2 are exhausted.

      Shelved (UX complexity rejected 2026-05-05): right-click → "Send
      to phone…" Finder Service that bypasses webdavfs entirely by
      having the bridge open the source file path directly. Works
      reliably for any size, but requires the user to learn a
      non-standard interaction. Keep as escape-hatch idea if all three
      above fail to land cleanly.

## Testing infrastructure

- [x] **Phone-side checksum verification.** Shipped 2026-05-11 as
      `make test-md5` / `test-md5.sh` per option 1 of the original
      ADB-shell-md5 analysis. Gated by `COMPRADOR_TESTING_ADB=1`
      env var so that ADB usage is explicitly developer-only — the
      shipping product still doesn't require Developer Options
      enabled on the user's phone. The script does
      `find <phone_dir> -exec md5sum` via adb shell, computes
      Mac-side md5 of the source tree, and reports per-file
      matches / misses / mismatches. AppleDouble `._*` files are
      excluded (filtered server-side per V0.3.3.md item #3).
      Verified against the 2026-05-11 ECON101 transfer: 430/432
      byte-perfect, the 2 deltas are both `.DS_Store` files that
      Finder legitimately regenerates at the destination.

- [ ] **End-to-end test harness (`test-e2e.sh`).** Builds on
      [docs/AUTOMATED-TESTING-DRAFT.md](docs/AUTOMATED-TESTING-DRAFT.md)'s
      survey of Mac automation surfaces and the recommended
      shell-first architecture. ~90 lines, no new dependencies.
      Composes with existing `test-md5.sh` for bulk-transfer
      verification.

      Flow: precondition the phone to MTP mode (via `adb shell svc
      usb setFunction`), wait up to 120s for the Comprador-mounted
      volume to appear under `/Volumes/`, smoke-list it, do a
      random-payload write+verify (`cp` + ADB md5 round-trip),
      do a read+verify (`cp` back + diff), clean up the phone-side
      artifact, eject via `diskutil unmount`. Report pass/fail.

      Key design decisions from the survey:

      - **Shell beats AppleScript for almost everything.** Five of
        six test needs are pure filesystem ops. `osascript` is only
        worth invoking if we later want to verify Finder's
        drag-and-drop verb *specifically* (which exercises the
        same bridge path as `cp`, so probably not).
      - **`diskutil unmount` beats `osascript`** for eject —
        no Automation permission prompt.
      - **No sudo needed.** `mount -t nfs` to localhost is
        unprivileged (Comprador's helper-free path).
      - **GUI session required** for any AX-based variants —
        rules out headless CI Macs, but the pure-shell pipeline
        runs fine without a logged-in session.
      - **Gate behind `COMPRADOR_TESTING_ADB=1`** matching the
        existing `test-md5.sh` convention.

      Makefile target: `make test-e2e` mirroring `make test-md5`.
      Expected duration: ~30 seconds per run if the phone is
      already in MTP mode + plugged in; longer if the mount has
      to be re-established. Suitable for pre-merge gates once
      stabilized, possibly with a `make test-quick` subset that
      skips the bulk transfer.

## Low impact (completeness)

- [ ] Multiple storage support (phones with SD cards → subdirectories under single mount)
- [x] Notarization build configuration (hardened runtime + signing for distribution)
- [ ] Large directory performance (700+ entries block the session goroutine; consider async/paginated enumeration)
- [ ] **Auto-spawn on USB connect (deferred).** macOS launchd supports
      `LaunchEvents → com.apple.iokit.matching` to spawn an agent when a
      matching USB device connects. Apple uses this for Image Capture etc.
      Wired through `SMAppService.agent`, it'd let users who quit
      Comprador have the app come back when they plug in a phone. But:
      "Start at Login" already covers this for the common case (most
      users enable it via the welcome window), the kernel-claim race
      against ptpcamerad is unaffected, and it adds a Login Items row.
      File this under "easy lever to pull *if* we see post-launch
      reports of 'I plugged in a phone and nothing happened.'" Until
      then, redundant.

## Known friction points

- [x] "Unsecured Connection" dialog on mount — fixed with `kNAUIOptionNoUI` + guest auth
- [x] Process names for the kill-before-claim were wrong: actual names on macOS Sequoia+
      are `ptpcamerad` / `AMPDeviceDiscoveryAgent` (not `PTPCamera`/`AMPDevicesAgent`).
      Kill is now done from inside the Go bridge with up to 6 retries to race
      launchd's ~60ms respawn window.
- [ ] PTPCamera must be killed before bridge can claim USB interface — works but inelegant
- [ ] `libusb_detach_kernel_driver` timeout adds ~5s on some connections
- [ ] **App-after-plug failure is unwinnable from any non-SIP-disabled path.**
      Diagnosed 2026-05-04 across multiple sessions; recording the dead ends
      so we don't re-walk them:

      Symptom: if Comprador starts *after* the phone is plugged in,
      `libusb_claim_interface` fails with `LIBUSB_ERROR_ACCESS` and stays
      failing across any number of retries, IOKit seizes, daemon kills,
      or USB resets. If Comprador is already running when the phone is
      plugged in, the bridge claims on attempt 1 — the bridge wins a
      race against `ptpcamerad`'s exclusive-access claim, but only in
      the first ~5–10 seconds after enumeration.

      Tried and failed:
      - `killall -9 ptpcamerad`: launchd respawns within ~60ms.
      - `IOUSBDeviceInterface500.USBDeviceOpenSeize`: returns
        `kIOReturnExclusiveAccess (0xE00002C5)`; IOKit refuses to evict
        an exclusive holder from userspace.
      - `libusb_detach_kernel_driver`: returns "Invalid argument";
        macOS doesn't support userspace driver detach.
      - `libusb_reset_device`: returns `LIBUSB_ERROR_NO_DEVICE`; the
        macOS reset path requires seized ownership.
      - `launchctl bootout gui/<UID>/com.apple.ptpcamerad`: refused
        with "Operation not permitted while System Integrity Protection
        is engaged". Even root cannot bootout Apple's
        `/System/Library/LaunchAgents` services with SIP on.

      Remaining options, all unacceptable for a consumer app:
      - Ship a kext (Apple deprecated kexts; user must disable SIP).
      - Ship a DriverKit extension (multi-week build, App Store review).
      - Tell the user to disable SIP.

      **Decision: accept the manual replug as the recovery path.** When
      the claim fails, the failure notification already tells the user
      to unplug and replug. The detach/attach cycle that follows fires
      a fresh USB enumeration, and the auto-retry path mounts cleanly.
      It's two seconds of physical action; not worth the consumer-hostile
      fixes above.
