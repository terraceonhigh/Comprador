# Comprador — TODO

## Navigation — where work lives

This file is the central backlog. Several adjacent docs track
specific kinds of work that don't belong here verbatim; check
all of them before assuming "is there nothing else?"

| Doc | Holds |
|---|---|
| [TODO.md](TODO.md) (this file) | Open items not tied to a specific release or plan. The default place for new work. |
| [docs/V0.3.3.md](docs/V0.3.3.md) | Per-release polish list. Item-numbered, ✓ marks shipped. When v0.3.3 cuts, create `docs/V0.4.0.md` for the next cycle. |
| [docs/PLAN-MULTI-STORAGE.md](docs/PLAN-MULTI-STORAGE.md) | Multi-storage feature plan. The §Sequence section is its TODO. |
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

## On-return pickups — autonomous session 2026-05-11

Items the autonomous afternoon session surfaced but couldn't
close without hands on the bridge. See
[correspondence/12-autonomous-afternoon-2026-05-11/letter.md](correspondence/12-autonomous-afternoon-2026-05-11/letter.md)
for full context.

- [ ] **Clean up the stale NFS mount** at `/private/tmp/comprador`.
      The bridge process (PID 79411) was killed mid-session per
      the architect's instruction; the mount entry on the kernel
      side persists. `sudo umount /private/tmp/comprador` to
      drop it before the next `make dev-nfs` cycle.
- [ ] **Diagnostic verification of MISTAKES 1a** (per-storage
      FSStat returning aggregate). Restart bridge with the
      diagnostic build (already at HEAD on `claude/multi-storage`):
      `make dev-nfs 2>&1 | tee build/dev-nfs.log`, mount, df both
      storages, grep the log for `FSStat path=`. Outcome
      determines whether plan option 1 sufficed or we need
      option 2 (encode storage in the NFS file handle). Detail
      in [MISTAKES.md entry 1a](docs/MISTAKES.md).
- [ ] **End-to-end verification of V0.3.3 #1** (TTL directory
      refresh / phone-side mutation surfacing). With the new
      bridge, `adb shell rm <file>` on the phone, wait ~2s,
      list the parent directory through the mount, confirm the
      file is gone. Logic is unit-tested
      (`make bridge-test`); the wire-up isn't.
- [ ] **Decide PR shape on `claude/multi-storage`.** 11 commits
      ahead of master. Each commit is independently reviewable;
      letter 12 has the chronological summary. Push and merge
      at the architect's pace.

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
