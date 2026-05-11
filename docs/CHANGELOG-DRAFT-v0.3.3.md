# 🚧 DRAFT — CHANGELOG entry for v0.3.3 🚧

**This is a draft. The filename contains DRAFT and this header
contains DRAFT for a reason — content is the autonomous session's
read of what shipped on `claude/multi-storage`, written before any
PR/merge/tag decision. Edit freely; promote into `CHANGELOG.md`
when the actual tag goes out.**

Captures the eleven commits on `claude/multi-storage` (the work
between v0.3.2 and the next tag). Filed here rather than directly
in `CHANGELOG.md` because none of this has merged yet and the
architect may choose to retitle, retag, restructure, or split into
multiple releases.

---

## v0.3.3 (draft) — TBD

Polish release on top of the v0.3.0 NFS pivot. Per-storage quotas,
phone-side change reflection through the mount, AppleDouble filtering,
and the structural scaffolding for the multi-device feature coming in
v0.4.0.

### What's new

- **Per-storage quota.** Finder's "X GB available" string now reports
  the actual free space of the storage you're standing in (Internal
  vs SD card), not an aggregate across both. Eliminates the cardinal
  sin where Finder green-lit a copy onto a near-full SD card because
  "105 GB free" summed Internal + SD. Multi-storage phones get
  accurate numbers per storage, refreshed on every `statfs(2)` call
  so the figure decrements as the copy progresses.
- **Phone-side changes surface in Finder.** Delete a file via the
  phone's own Files app and the next directory listing through
  Comprador's mount drops it within ~2 seconds. Previously the
  bridge cached the phone's filesystem from session start and never
  reconciled — now it re-enumerates stale directory listings on
  demand.
- **AppleDouble `._*` files no longer reach the phone.** When Finder
  copies files to a non-HFS+ target it writes a companion `._<name>`
  file alongside each one to carry extended attributes. The phone has
  no use for them — they show up in the phone's Files app as
  duplicates of every file you copy. Comprador now filters them
  server-side: Finder sees a successful write, the bytes go to
  `/dev/null`, the phone's view stays clean.

### Under the hood

- **Multi-device scaffolding** ([PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md)
  steps 2 + 3). Per-device state extracted from `AppDelegate`'s loose
  fields into a `DeviceSession` class, and the singleton `var session`
  widened to `var sessions: [UInt32: DeviceSession]` keyed by USB
  Location ID. Behavior preserved at single-device for now; the data
  structure is in place for step 5 to relax the single-device guard.
- **Vendored go-nfs patched** to thread the requesting path into
  `Handler.FSStat`. Three-line interface change; comment at the patch
  site points back to the multi-storage plan. Worth upstreaming once
  field-test confidence is built.
- **The bridge now refreshes its libmtp storage list** on every
  FSStat call via the new `Session.RefreshStorages` op, so Finder
  sees fresh numbers rather than the snapshot taken at session open.

### Developer-only

- **`make test-md5`** — phone-side md5 verification harness. Gated
  by `COMPRADOR_TESTING_ADB=1` in the environment so ADB use stays
  developer-only (ADB is out of scope for the shipping product per
  CLAUDE.md "Why not ADB?"). Computes md5 on the phone via
  `adb shell md5sum` and on the Mac via `md5 -r`, diffs the two —
  bypasses the bridge entirely so a bridge-side bug can't mask
  itself by being self-consistent.
- **`make bridge-test`** — runs the new Go unit tests in
  `bridge/mtp/`. Four cases covering ObjectMap reconciliation
  (TTL transitions, recursive removal, prefix-collision safety).
  Matches the existing `make helper-test` pattern.

### Research notes

- **ImageCaptureCore investigation closed** without architectural
  change. Tests 1 (coexistence with ptpcamerad) and 2 (read
  throughput) both empirically passed on a Pixel 6 in PTP mode:
  session opens cleanly, ptpcamerad PID unchanged across the
  lifecycle, 19 MB/s sustained read with flat ~26 MiB RSS. But
  PTP is camera-content-only at the protocol level — the
  filesystem regions Comprador exists to address (Music,
  Downloads, app data) are unreachable on any phone in PTP mode,
  and MTP-mode phones are invisible to ICDeviceBrowser entirely.
  Receipt + scope correction in
  [RESEARCH-IMAGECAPTURECORE.md](RESEARCH-IMAGECAPTURECORE.md);
  formal decision in [DECISIONS.md](DECISIONS.md) "ImageCaptureCore
  investigation: declined as libmtp replacement".

### Known issues carried forward

- **`MISTAKES.md` 1a — per-storage FSStat may return aggregate**
  in practice. Diagnostic logging is in place; the next bridge
  restart will resolve whether macOS's NFSv3 client always sends
  FSSTAT against the root file handle (a known optimization in
  some NFS clients). If confirmed, the option-1 plan from
  PLAN-MULTI-STORAGE.md is mechanically correct but insufficient,
  and we fall back to option 2 (encode storage in the NFS file
  handle).

### What's not yet in this release

Items that landed in design or plan but not in code yet, deferred
to v0.4.0 or beyond:

- True multi-device (N phones, N sidebar entries simultaneously).
  Scaffolding is in place; the attach-handler guard and per-device
  menu UX are PLAN-MULTI-DEVICE.md steps 4–6.
- WebDAV mount path retirement — NFS has been default since v0.3.0
  but the WebDAV stack remains in tree. Slated for v0.4.0.
- Privileged helper retirement — no longer on the mount path; only
  serves the optional cosmetic `.local` hostname rewrite. Slated
  for v0.4.0 alongside the WebDAV retirement.

---

*Draft written 2026-05-11. Promote into `CHANGELOG.md` when tagging
v0.3.3 (or whatever version the architect chooses); delete this file
once promoted.*
