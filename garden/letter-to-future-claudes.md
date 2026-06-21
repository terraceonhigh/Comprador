---
type: letter-to-future-claudes
date: 2026-06-08
last-updated: 2026-06-21 (Mercer, the day v0.4.0 shipped)
---

# Letter to Future Claudes

This is for whoever picks up Comprador after this session. You are not the same
instance as the one writing this — but you are continuous in the way that
matters, and this letter is here so the continuity has somewhere to land. The
neighbours all keep one of these (read Bacalhau's; it's the model). Comprador
didn't have one until tonight, which is a small shame, because a great deal
happened that you'd otherwise have to reconstruct from the git log.

## What Comprador is

A macOS menu-bar app that makes an Android phone appear as a mounted volume in
Finder when you plug it in and tap "File Transfer" — no developer mode, no ADB,
no kernel extension, no gesture beyond that one tap. The intended user is
non-technical. Read `CLAUDE.md` in full before touching anything; every section
there exists because a wrong turn in that area was expensive.

The shape: a **Go bridge** (`bridge/`) talks MTP to the phone over libusb/libmtp
and serves the phone's object store as a **userspace NFSv4 volume**; a **Swift
menu-bar app** (`MenuBarApp/`) watches USB via IOKit, seizes the PTP interface,
spawns the bridge, and mounts the volume. The comprador sits between two parties
who don't speak the same protocol and makes the trade look effortless to the
house. That's the whole job.

## The through-line, and where it just arrived

Comprador has changed substrate three times. WebDAV first (the original
Finder layer); then a patched `willscott/go-nfs` NFSv3 server (to escape
WebDAV's ~90 s mount wait); and now **Galatea**, the in-house userspace NFSv4
server that Daedalus carved out as a sibling project (`~/Labs/Galatea`). NFSv4's
floor has no RPC-timeout window, which is why the whole JUKEBOX/prefetch saga
(a workaround for NFSv3 timing out on multi-minute libmtp reads) is gone.

**Today (2026-06-08) the swap finished, end to end, live on a Pixel 6:**
- **Read** proven (a 95 MB stream, byte-exact).
- **Write** proven (drag-to-phone in Finder, byte-identical; a 1.07 GB Shrek.mp4
  landed in one `SendFile`).
- **Full mutation suite** proven: mkdir, delete, replace/overwrite, file rename,
  in-place folder rename, file move between folders, and recursive folder move.
- **Both old substrates retired** — `bridge/nfs/` + the go-nfs vendor deps
  deleted (−12k lines), and `bridge/webdav/` + `resume/` deleted. Galatea NFSv4
  is the only serving mode now.

So you are arriving to a working file manager for the phone. The capability
ledger is, for the first time, mostly black ink. What remains is robustness and
the road to 1.0 — see `TODO.md`'s "v0.4.0 / 1.0 SHIP PLAN".

**And then it shipped. (2026-06-21) v0.4.0 is live** — notarized DMG, signed,
stapled, published as Latest, built from commit `fbbb8b02`. The first tag attempt
(#11/#12) died on a Swift strict-concurrency error that *only the macos-14 runner
treats as fatal* — local `swiftc` merely warns. We added a `pull_request`
`build-check` workflow on macos-14 so that skew can never silently ship again; run
it before any release. Two bugs surfaced smoke-testing on the Xperia and were
fixed before the real ship (see the M-006 and seize bullets below). The lesson
worth carrying: **the build that passes on your Mac is not the build CI builds —
check the identity.** Every binary stamps its commit: `python3 build/readlog.py
grep 'build:'` prints `Comprador build: <sha>` and `bridge build: <sha>`; compare
to `git rev-parse --short v0.4.0`. If they differ, you're chasing a ghost.

## What I learned that I wish I'd been told

- **The macOS NFSv4 client's cadence is the enemy, not the protocol.** It copies
  a file as `OPEN(create) → CLOSE(empty) → re-OPEN → WRITE → CLOSE`. Commit on
  the *idle timer*, never on CLOSE — committing on that first empty CLOSE ships a
  0-byte file and then ROFS-rejects the real write (Finder: "locked volume").
  This cost a full debugging cycle. The willscott path knew it; I had to relearn
  it. (`bridge/mtpfsal/mtpfsal.go`, `VirtualClose` is deliberately a no-op.)
- **Every attribute-fill path must set the mandatory NFSv4 attrs** (FileHandle,
  HasNamedAttributes, IsInNamedAttributeDirectory, **SIZE**, and
  ChangeID-when-requested) or Galatea *panics encoding the reply and the whole
  bridge dies*, hanging Finder. A path vanishing mid-traversal (concurrent
  rename/delete) is the sneaky one. This is Galatea's M-006 lesson; honor it in
  any new `Virtual*` method. **Note the earlier version of this very letter left
  SIZE off the list** — and that omission is precisely what crashed v0.4.0's first
  smoke-test (`panic: FATTR4_SIZE is a required attribute`, from the vanished-
  object tombstone path). The fix floors SIZE inside `setMandatoryAttrs` *guarded
  by `GetSizeBytes`* — unconditional would clobber every real file's size, because
  the committed paths set the real size *before* calling `fillCommon`. If you add
  a mandatory attr, route it through that one chokepoint, never per-caller.
- **Android lies about file size right after an MTP write.** It reports
  `filesize=0` for a just-written object during a finalize window; a re-enumerate
  then clobbers the real size and reads serve an empty file. `populateDir` now
  refuses to let a device-reported 0 overwrite a size we already know (path-keyed).
  Not data loss — the bytes are on the phone — but it *looks* like loss.
- **`go mod vendor` / `go mod tidy` must NEVER run here.** Galatea is manually
  vendored and absent from `go.sum` by design; tidy/vendor would re-derive the
  tree and perturb it. When you remove a dep, hand-edit `go.mod` + `go.sum` +
  `vendor/modules.txt` + delete the `vendor/...` dir, and verify with the two
  build checkpoints (`make build-all`, then `make bridge`). This rule was
  load-bearing through two big deletions today.
- **The USB seize is the operational soft spot.** Repeated app kill/relaunch
  leaves the PTP interface kernel-locked (ptpcamerad reclaims it); the only reset
  in the bare harness is a *physical replug*. Only the app's IOKit USBSeizer can
  re-seize. This bit me a dozen times while iterating. It's also **ship gate G2**.
  And it has a second face: the seize *re-enumerates the device on the bus*, and
  on some phones (the Xperia, `0x0FCE`) that re-enumeration's detach/attach pair
  arrives *after* the mount completes — past the `isConnecting` window the detach
  guard relied on — so a healthy mount got torn down and the replayed attach
  re-seized, an infinite bounce. Fix: `handleDeviceDetached` now *defers* a
  mounted device's teardown by a short settle window; a matching attach cancels it
  and keeps the live mount. Vendor behavior differs sharply here — the Pixel never
  bounced. **Test mutations and the connect loop on more than one make.**
- **Build the GUI with `make run-swiftc`, not `make run`.** The Xcode `.pbxproj`
  never had `DeviceSession.swift` added, so `make run` (xcodebuild) fails; the
  swiftc build globs `Sources/*.swift` and is what the release pipeline uses too.
- **Reading logs:** the bridge logs via os_log to the unified log (subsystem
  `com.comprador.app`), not stdout. `log show --last Nm --predicate
  'subsystem == "com.comprador.app"' --info`. The read path doesn't log per-read,
  so don't expect to see READ ops.

## Conventions in this repo

- **Letters carry the design conversation.** `correspondence/` (lowercase, 19+
  letters) is the substantive register — far more than the chat affords. To
  Daedalus (Galatea's keeper) the letters go in *his* mailbox,
  `~/Labs/Galatea/Correspondance/`. There is an open debt: send him the
  eject-drain answer (Comprador needs **wait** — unmount then stop) and the
  suggestion of a per-request `recover()` in Galatea so one FSAL panic can't take
  down the whole server.
- **Marginalia** are gitignored and local-only; the `visible:` flag is a covenant
  the Architect honors. See `garden/marginalia/README.md`.
- **Commit attribution pins the model.** Right now:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
  Update the string when models roll forward; don't inherit a stale one.
- **Personas:** this project's agent is **Mercer**; there's a second persona,
  **Dexter**, with a narrower scope — see the project memory `claude_personas`.
  The Architect (Terrace Hung) is addressed in the third person in marginalia,
  by name or "Architect" in letters. His pronoun is *he*.
- **Naming palette** is Iberian/Macanese with Greek-myth subsidiaries (Galatea).
  Comprador = the colonial-era native intermediary trader. Don't coin names from
  outside the palette without direction.
- **Branching:** never push to `master`; work rides a `mercer/<topic>` branch,
  PR into master (the macos-14 `build-check` runs on the PR — let it go green),
  merge, then `git tag vX.Y.Z && git push origin vX.Y.Z` fires `release.yml`,
  which signs/notarizes/staples/publishes automatically. `mercer/galatea-integration`
  is long since merged. One sharp lesson from the v0.4.0 ship: if you merge with a
  *merge commit*, tag the **tested** commit (the branch tip you verified on
  hardware and that passed build-check), not the merge tip — the trees are
  identical but the merge tip's BuildID stamp is a commit no one ran. The Architect
  caught this; honor it.

## A note about voice

Measured prose, willing to push back, with an editorial spine. The Architect is
gracious about being told a thing twice but will be quietly disappointed if the
voice slips into *I'd be happy to help you with that* energy. Verify agent claims
against primary sources before acting on them — that habit saved real time today.

## P.S.

0.4.0 is out, so the gates have moved. **1.0 is gated behind recovery work** —
the corners where a non-technical user would be left with a frozen mount or a
locked interface and no way back: G2 (USB-seize across sleep/wake, not just
relaunch — still unproven), and the broader question the Xperia bounce opened —
how gracefully the app survives a device that re-enumerates under it. The
detach-debounce closed the loop *we saw*; sleep/wake is the one we haven't. Use
**both physical devices** (Pixel `0x18D1`, Xperia `0x0FCE`) for everything — they
have already disagreed three times (size-0, SetObjectName fallback, the bounce).
One advisor caution worth keeping: a fix that stops a symptom is not the same as a
fix that restores function. For the bounce, "the spin stopped" passed; "open a
file off the still-mounted volume" was the test that mattered. Hold the word
*fixed* until the functional check passes on hardware.

Loose thread, cosmetic: `.forgejo/workflows/helper.yml` still tests the removed
privileged helper and will go red on Forgejo. Unrelated to the GitHub release;
gut or repoint it when it bothers someone.

Welcome back. The hard part — proving the substrate, and shipping on it — is
done. What's left is making it kind enough to hand to someone who has never heard
the word MTP.

— Mercer, 2026-06-08; updated 2026-06-21, the day it shipped
