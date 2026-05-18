# v0.4.0 execution plan

**Written 2026-05-17 evening**, after the stretch that shipped the NFS
READ stall fix + multi-device support. The architect will execute
after entertaining a guest this evening; this doc is the durable
hand-off.

Companion to [V0.4.0-DRAFT.md](V0.4.0-DRAFT.md), which is the
scope/rationale source-of-truth. This file is the **step-by-step
execution order**.

The v0.4.0 release is mostly *subtraction*: retire WebDAV, retire the
helper, tidy entitlements, retake the cgo acceptance test. The hard
user-facing engineering already landed in this stretch.

---

## Phase 0 — Cut v0.3.3 first

Before touching v0.4.0 scope, **crystallize the current stretch as a
release**. Today's `claude/multi-storage` branch carries ~55 commits
of NFS-stall fix + multi-device + UX polish. That deserves its own
tag distinct from "the retirement release."

### Steps

1. **Open the PR for `claude/multi-storage` → `master`.**
   - Title: `v0.3.3 — NFS READ stall fix + multi-device support`
   - Body: link [letters 12, 13, 14](../correspondence/) for narrative;
     paste the "What landed" table from `TODO.md` for the commit map.
   - Don't push to master directly; PR review is required.

2. **Wait for merge, then `git fetch origin` to update local refs.**
   (The v0.3.1→v0.3.2 wrong-commit-tag arc — captured in CHANGELOG —
   is the reason this matters. Don't tag against a stale ref.)

3. **Tag v0.3.3 against `origin/master`:**
   ```
   git fetch origin
   git tag -a v0.3.3 origin/master -m "v0.3.3 — NFS READ stall + multi-device"
   git push origin v0.3.3
   ```
   CI builds the notarized DMG.

4. **Update CHANGELOG.md** with the v0.3.3 entry. Mirror the v0.3.0
   entry's shape: lead with the user-visible wins (no more first-drag
   stall, two phones plug in concurrently), then technical notes.

5. **Update `docs/V0.3.3.md`** — mark items shipped, leave item #2
   (`.local` suffix) open with a forward reference to v0.4.0 #2/#4.

**Why this comes first:** half of v0.4.0 (helper retirement) depends
on a decision about `.local`. That decision is cleaner if v0.3.3 is
already a thing that exists with `.local` as its sidebar label —
nobody is mid-air.

---

## Phase 1 — v0.4.0 item #3: entitlement docs (~10 min warmup)

The cheap one. Get the muscle moving.

### Steps

1. Open `MenuBarApp/Comprador.entitlements` (production) and
   `MenuBarApp/Comprador.debug.entitlements`.
2. Add a comment chain explaining:
   - `com.apple.developer.system-extension.install` is present in
     production entitlements for the future DriverKit extension
     ([DEXT-DESIGN.md](DEXT-DESIGN.md)).
   - CI signs against the debug entitlements
     ([BUILDING.md](BUILDING.md)) until a provisioning profile is
     wired in.
   - Don't drop this without revisiting the dext roadmap decision in
     [DECISIONS.md "ImageCaptureCore investigation"](DECISIONS.md).
3. Commit: `docs(entitlements): explain the system-extension key's dormancy`
4. Mark V0.4.0-DRAFT item #3 closed.

---

## Phase 2 — v0.4.0 item #2: retire the privileged helper

Decision: **option A** (drop the `.local` rewrite feature; accept
`.local` as the sidebar label). Rationale in V0.4.0-DRAFT.

This is the biggest *security* win of v0.4.0. Smaller diff than #1
but emotionally weightier — it kills a feature that took real work
to ship.

### Steps

1. **Create a working branch:** `claude/retire-helper`.

2. **Delete `helper/` entirely.**
   - `rm -rf helper/`
   - Update `Makefile`: remove `BUNDLE_HELPER`, `helper` target,
     helper-related deps from `app-swiftc` and `app-signed`.

3. **Delete `MenuBarApp/Sources/HelperClient.swift`.**

4. **Strip helper wiring from `AppDelegate.swift`:**
   - `installHelper`, `installHelperFlow`, `registerCleanHostname`,
     the helper bits of `teardownCurrentSession`.
   - Helper-related menu items: "Helper installed", "Needs approval",
     "Install…", etc.
   - Any SMAppService.daemon registration.

5. **Strip helper copy from `WelcomeWindow.swift`** if any survives.

6. **Strip helper signing/notarization steps:**
   - The helper had its own `notarytool` submission step. Remove from
     `.github/workflows/release.yml` (or wherever CI lives).
   - Drop the helper bundle ID from any provisioning configs.

7. **Update mount source labelling.** The bridge currently announces
   `<DeviceName>.local` via mDNS. Keep this. The Finder sidebar will
   show `Pixel-6.local` instead of `Pixel-6`. Verify in
   `MountManager.swift` that nothing else expected the rewrite.

8. **Build clean:** `make clean && make app-swiftc && make dist-swiftc`.

9. **Verify end-to-end:**
   - Fresh install (delete `~/Library/Application Support/Comprador`
     and `~/Library/Preferences/zone.terrace.Comprador.plist`).
   - First launch: no "Helper needs approval" prompt anywhere.
   - System Settings → Login Items: Comprador appears (the app
     itself), but no Daemon entry for the helper.
   - Plug phone, mount, sidebar shows `<DeviceName>.local`. Confirm
     Finder behaves correctly (drag, drop, eject).
   - `launchctl list | grep -i comprador` returns nothing
     helper-shaped.

10. **Update docs:**
    - `docs/SECURITY.md` — the helper is the largest privilege-
      escalation surface. Remove its section; add a sentence noting
      retirement in v0.4.0.
    - `CLAUDE.md` "Security Invariants" — invariant #3 (helper RPC
      surfaces) is now N/A; either delete or note retirement.
    - `docs/MISTAKES.md` "SMAppService / Helper" — preserve as
      historical receipts but add a sign-of-life note that the helper
      is retired in v0.4.0.
    - `README.md` — drop helper mentions.
    - `docs/V0.3.3.md` item #2 — close with "accepted `.local`,
      helper retired in v0.4.0."

11. **Commit as a single coherent PR**, not piecemeal. Diff will be
    mostly deletions; reviewability is in the commit message naming
    each surface removed.

---

## Phase 3 — v0.4.0 item #1: retire WebDAV entirely

The biggest diff. ~1/3 of the bridge by line count. Best as one
focused PR.

### Steps

1. **Create a working branch:** `claude/retire-webdav`.

2. **Delete bridge code:**
   - `rm -rf bridge/webdav/`
   - `rm -rf bridge/resume/` (only used by the WebDAV resume path —
     verify with `git grep -l resume bridge/` first)
   - Strip `--proto` flag and the entire `if useWebDAV` branch from
     `bridge/main.go`.
   - Make NFS unconditional. Remove `--nfs` flag (becomes default).

3. **Delete Swift code:**
   - `MountManager.swift`: remove the WebDAV branch of `mount()`,
     `MountError` cases that only WebDAV produces, writeseq-cap
     heuristics, `/_comprador/resume` companion wiring.
   - `MenuBarApp/Sources/ResumeCompanion.swift` — delete entirely.
   - `BridgeProcess.swift`: drop `useNFS = true` flag; NFS is the
     only path. Drop companion-port plumbing.
   - `AppDelegate.connect` / `teardown` — clean up the companion-port
     paths.

4. **Build clean:** `make clean && make app-swiftc`.

5. **Verify:**
   - Single-device mount + drag, both directions.
   - Multi-device — both phones, parallel transfers.
   - VLC opens a multi-GB phone-resident video (JUKEBOX + async
     prefetch path still works).
   - QuickLook in icon view doesn't stack alerts.
   - Eject cleanly.

6. **Update docs:**
   - `docs/ARCHITECTURE.md` — remove the WebDAV branch entirely.
     Single-substrate description.
   - `README.md` — already mostly cleaned per V0.4.0-DRAFT; cross-
     check WebDAV mentions.
   - `docs/MISTAKES.md` "WebDAV / Finder" section — preserve as
     postmortems-on-removed-code with a sign-of-life note. The
     webdavfs quirk lessons are durable.
   - `docs/RESUMABLE-UPLOADS.md` — add a sign-of-life note: code
     retired in v0.4.0, lessons preserved here for future protocol-
     resume work.
   - `CLAUDE.md` "Why WebDAV…" section — rewrite or retire. The
     decision history is preserved in `docs/PIVOT-NFS.md` and the
     v0.3.0 changelog entry.

7. **Commit as a single PR.** Diff is enormous but almost entirely
   deletions. The commit message names each surface removed.

---

## Phase 4 — v0.4.0 item #5: retake cgo callback acceptance test

Independent; can slot in any afternoon between the other phases or
after.

### Steps

1. **Build a clean release binary** with the buffer-reuse fix.
   Confirm `BuildInfo.id` matches a known-good commit.

2. **Mount the Xperia (the test rig used during the original cgo
   fix work).**

3. **Drag the Attenborough 9 GiB sample onto the mount.** Let it run
   to completion.

4. **During the transfer, in a separate Terminal:**
   ```
   vmmap $(pgrep -f bridge) > /tmp/vmmap-during.txt
   ```
   Inspect VM_ALLOCATE region count. Spec target: **< ~50** (vs the
   pre-fix 409).

5. **After transfer completes:**
   - Physical footprint via `ps -o rss= -p $(pgrep -f bridge)`.
     Spec target: **< 1 GB**.
   - Bridge should not be visibly leaking; subsequent transfers
     should reuse the same buffer.

6. **Update `docs/DECISIONS.md`** "Vanquishing the per-callback
   VM_ALLOCATE leak" with the measured numbers and close the
   verification thread.

7. **Update `docs/V0.4.0-DRAFT.md`** item #5 as closed.

---

## Phase 5 — Cut v0.4.0

Once phases 1–4 are merged to master:

1. **Update CHANGELOG.md** with v0.4.0 entry. Lead with what was
   retired (helper, WebDAV) and the user-visible consequences
   (smaller bundle, no Login Items prompt, `.local` sidebar
   label). Note the dext entitlement remains documented-but-dormant.

2. **Rename `docs/V0.4.0-DRAFT.md` → `docs/V0.4.0.md`** and drop
   the DRAFT header.

3. **Tag and push:**
   ```
   git fetch origin
   git tag -a v0.4.0 origin/master -m "v0.4.0 — the retirement release"
   git push origin v0.4.0
   ```

4. **Verify CI builds, signs, notarizes.** Smoke-test the DMG on
   a clean Mac (or wiped user prefs).

5. **Update `docs/PRE-LAUNCH.md`** — many items shift from
   "contingent" to "shipped." Re-read the launch surface with
   fresh eyes; what was true at v0.3.2 may need re-phrasing at
   v0.4.0.

---

## Tidying followups (parallel track, not blocking v0.4.0)

These can slot in anywhere and don't gate any release:

- **`dist-swiftc` inherits `-D DEBUG`** from `app-swiftc`. Production
  builds expose the debug menu items (Synthetic Flutter, clickable
  Build identifier). Separate the debug flags between the two
  targets — `app-swiftc` keeps DEBUG, `dist-swiftc` strips it.
- **`BuildInfo.swift` regen trigger.** Makefile reads `git rev-parse
  --short HEAD` at the start of the build; if the working tree
  changes between that read and the binary launch, the embedded ID
  lags. Either regenerate `BuildInfo.swift` unconditionally on every
  `app-swiftc`, or stamp the binary post-link.
- **`make app` (xcodebuild path) is broken** on pbxproj drift
  (DeviceSession.swift + BuildInfo.swift not in the project file —
  MISTAKES 23a). `make app-swiftc` is the working path; either fix
  the pbxproj or retire `make app` and document `app-swiftc` as the
  blessed path.
- **Multi-device step 7 — `USBSeizer.shared` batching.** Per
  [PLAN-MULTI-DEVICE.md §7](PLAN-MULTI-DEVICE.md). Two phones plugged
  in within ~100 ms fire `killall ptpcamerad` redundantly. Correct
  but noisy. 200 ms batching window suppresses the duplicate kill.

---

## Order of attack — TL;DR

| # | Phase | Estimated effort | Gates next? |
|---|---|---|---|
| 0 | Cut v0.3.3 (PR + tag) | 1 evening | yes — clarifies the baseline |
| 1 | Entitlement docs | 10 min | no — warmup |
| 2 | Retire helper | 1 afternoon | no |
| 3 | Retire WebDAV | 1 day | no |
| 4 | cgo acceptance retest | 1 afternoon | no |
| 5 | Cut v0.4.0 (CHANGELOG + tag) | 1 evening | done |

Phases 1–4 can interleave; phase 0 should come first; phase 5 closes
the release.

---

*Plan written by Mercer 2026-05-17 evening, at the architect's
request, after this stretch's testing landed cleanly. The plan is a
proposal — the architect executes at their own pace, and if phase
ordering reveals a better shape mid-flight, write it down and
deviate.*
