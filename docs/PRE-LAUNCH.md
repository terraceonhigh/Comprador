# Pre-launch readiness — Reddit / Hacker News

**Status — 2026-06-21:** Comprador is at **v0.4.0** — shipped (a
notarized, signed, stapled DMG is live on
[GitHub Releases](https://github.com/terraceonhigh/Comprador/releases/tag/v0.4.0),
built from commit `fbbb8b02`) and working, but **not yet publicly
announced**. This doc tracks the remaining path from "shipping
quietly to early users" to "ready for a Reddit / HN post." The
landing page is live at
**https://terraceonhigh.github.io/Comprador/** (source in
[docs/site/](site/)); the discovery and channel strategy lives in
[docs/SEO-PLAN.md](SEO-PLAN.md), which is the companion to this doc
for the announcement itself.

This doc gets updated as the launch approaches. The structure
below is the durable shape; the contents change.

## What we'd be announcing today (v0.4.0 baseline)

A working macOS app that mounts Android phones (and PTP/MTP
cameras) as Finder volumes. Plug in, tap File Transfer, the
phone appears in the Locations sidebar — then **browse, copy off,
copy on, delete, rename, and reorganise** files in place, all
through Finder. ~7 MB notarized DMG, macOS 13+, Apple Silicon only.

The Finder layer is **Galatea**, an in-house userspace **NFSv4**
server the Go bridge runs over loopback; the Swift menu-bar app
mounts it with `mount -t nfs`. No kernel extension, no macFUSE, no
privileged helper — the earlier WebDAV server, the patched
`willscott/go-nfs` NFSv3 path, and the root helper were all
**removed in v0.4.0** (see [CHANGELOG.md](../CHANGELOG.md)).

### Defensible claims today

- **Phone-in-Finder, plug-and-play.** No installer ceremony
  beyond drag-to-Applications. No Developer Options on the
  phone. No kernel extension. No SIP disable. No subscription.
  No account.
- **Full two-way file management.** Browse, copy off, copy on,
  delete, rename (instant, in-place), create folders, move files
  and whole folders — all in Finder. Verified live on a Pixel 6:
  a 1 GB read and a 1.07 GB single-transfer write, byte-identical.
- **Notarized.** First launch is just a double-click; no
  right-click → Open dance.
- **No telemetry, no phone-home.** The bridge binds to
  loopback. No outbound network connections. Explicit in
  [SECURITY.md](SECURITY.md).
- **Native menu bar app.** ~7 MB DMG, not Electron, not a web
  view, not a 360 MB shell.
- **NFSv4 mount (Galatea), sub-second mount time.** No 90-second
  WebDAV preflight wait. The mount feels like a USB stick.
- **Works with cameras too.** Anything libmtp recognizes as
  MTP- or PTP-class. Tested with phones (Sony Xperia, Pixel)
  and PTP cameras (Canon, Nikon, Sony, Fuji).

### Things that will get probed and our answers

| Question | Answer |
|---|---|
| Why not File Provider? | MTP is stateful, session-locked. File Provider's pull-based model doesn't fit. Detailed: [CLAUDE.md "Why not File Provider API?"](../CLAUDE.md). |
| Why not macFUSE? | Kernel extension. Needs SIP disable or Gatekeeper navigation. Unacceptable for the user model ([USER.md](USER.md)). |
| Why not ADB? | Requires Developer Options on the phone. Friction we will not impose. |
| Why GPL and not MIT? | Inherited GPL by way of an OpenMTP fork; cleaned all OpenMTP code, severed the fork; could relicense MIT now but haven't bothered. Not actively defended; might revisit. ([NOTICES.md "Historical: OpenMTP"](../NOTICES.md)) |
| How does this compare to OpenMTP? | Different shape entirely. OpenMTP is an Electron app window. Comprador is a Finder mount. Same problem domain, opposite integration strategy. ([OPENMTP-NOTES.md](OPENMTP-NOTES.md)) |
| How does this compare to SwiftMTP? | SwiftMTP is a native app window; Comprador is a Finder mount. Both are smaller than OpenMTP. SwiftMTP added a Foundation Models-based "AI search" feature; we have no such ambition. ([SWIFTMTP-NOTES.md](SWIFTMTP-NOTES.md)) |
| Why a Go subprocess instead of in-process? | Subprocess gives us crash isolation, clean process boundaries, and (as we discovered) makes true concurrent multi-device tractable in a way the in-process-cgo references can't easily match. ([SWIFTMTP-NOTES.md "JSON-over-cgo as Swift↔Go interface"](SWIFTMTP-NOTES.md) tradeoff section.) |
| Does it work with iPhones? | No. iPhones don't speak MTP. Use Image Capture / Photos / iCloud / Finder for those. |
| Does it work over Wi-Fi? | No. USB only. Wireless MTP is technically possible but the protocol surface roughly doubles for little gain. |
| What's the catch? | Listed below under "Known issues we will disclose." |
| Is it safe? | Threat model at [SECURITY.md](SECURITY.md). Loopback-only NFS, no telemetry, library validation disabled (for bundled dylibs), USB entitlement scoped to known MTP vendors. The privileged root helper was **removed in v0.4.0** (loopback NFS mounts unprivileged, so it was vestigial) — there is no longer any root component or admin-password prompt. |
| Why's it called Comprador? | Portuguese-origin term for the native intermediary in Western trading firms operating in colonial-era Macau and Canton. The project takes its visual palette from Iberian/Macanese vernacular (the logo is a 1705 Merian engraving — see [NOTICES.md "Logo"](../NOTICES.md)). |

### Known issues we will disclose

Be honest. These are the things people will hit. Disclose
proactively rather than letting comment threads surface them.

- **A USB-interface lock can still require a physical replug
  across system sleep/wake.** v0.4.0 narrowed the old
  first-plug-after-launch failure considerably: the app
  self-heals when the bridge dies (detects the exit, unmounts
  the stale volume, re-spawns and remounts), and an
  orphaned-bridge reaper lets a relaunch seize the USB interface
  without a physical replug. The residual case is the
  `ptpcamerad` claim race specifically after sleep/wake, where a
  replug is still sometimes needed. The
  [ImageCaptureCore research](RESEARCH-IMAGECAPTURECORE.md) is
  the candidate permanent fix.
- **Apple Silicon only, macOS 13+.** Intel Mac and older macOS
  are not supported. Intel is not on the roadmap; older macOS
  may be a future ask.
- **Per-storage quota may be aggregated.** v0.4.0 reports accurate
  free space to Finder so drag-and-drop pre-flight works; whether
  phones with SD cards still report a single aggregate across
  storages (rather than per-storage) is not separately confirmed.
  Fix plan in [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md).
- **No auto-update.** Manual DMG download from GitHub Releases.
  Trade-off: also no silent supply-chain attack vector.
  Sparkle-style auto-update is on the longer-term roadmap.

## What's already in vs. still ahead

### Already landed (shipped in v0.4.0)

- **Multi-GiB transfers no longer leak memory.** The cgo callback
  buffer-reuse fix landed (verified 2026-05-14: ~67 `VM_ALLOCATE`
  regions, ~8 MB RSS after a 9 GiB transfer, versus the pre-fix
  409 regions). Bridge footprint stays bounded regardless of file
  size. The full-scale 9 GiB acceptance retest is still an open
  verification follow-up in [TODO.md](../TODO.md), but the fix
  itself is in.
- **Concurrent multi-device — verified working on hardware.** Two
  phones (or a phone and a camera) plugged in at once, each its own
  Finder Locations entry, browseable in parallel; every device gets
  its own bridge subprocess, mount, and quota. No in-tree reference
  (OpenMTP, SwiftMTP) does this concurrently.
- **Stream media straight off the phone.** The NFSv4 floor tolerates
  multi-minute reads, so you can play a video or scrub through it in
  place — watch a documentary off the phone without copying it to the
  Mac first. The retired NFSv3 path timed out on exactly this.

### Still ahead

- **Per-storage `statfs(2)`.** v0.4.0 reports accurate aggregate
  free space; per-storage reporting for SD-card phones is the
  remaining refinement. Plan in
  [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md).

### Contingent on ImageCaptureCore tests

Four empirical tests sketched in
[RESEARCH-IMAGECAPTURECORE.md](RESEARCH-IMAGECAPTURECORE.md).
If they pass:

- **No more `killall ptpcamerad`.** The seizure race
  disappears. App-after-plug failure becomes a historical
  curiosity.
- **DriverKit dext is canceled.** The dext exists to win the
  kernel-claim race; if we don't fight, we don't need it.
- **App Store distribution becomes plausible.** Currently
  sandbox-blocked by the kill operation.

(The privileged helper is already gone, removed in v0.4.0 — that
strand of the original investigation is settled independently of
these tests.)

If they fail, nothing changes architecturally — we keep
libmtp + seizure + dext-on-roadmap. The doc becomes a closed
investigation.

## The pitch

A two-sentence version for the post title and first paragraph:

> Comprador mounts your Android phone (or PTP camera) as a real
> Finder volume — browse, copy, delete, and rename in place. Plug
> in, tap File Transfer, done. Free and open source, no app window
> to babysit, no kernel extension, no developer mode, no Electron,
> no telemetry, no subscription.

The differentiator is the **combination**, not any single leg.
"Native Finder mount" alone isn't unique (MacDroid markets it;
FUSE/CLI tools do it); free-and-OSS alone isn't (OpenMTP is). What
no rival offers is the whole stool at once — free + open-source +
a *true Finder volume* (not an app window) + nothing to open or
babysit + no kernel/system extension + no developer mode + and
still consumer-grade (notarized, auto-detecting menu-bar app). See
the per-rival comparison grid in
[docs/SEO-PLAN.md §3](SEO-PLAN.md); that table, and the channel
plan in §5, are the source for the announcement copy — don't
re-derive them here.

Positioning sentence the research supports:

> Your phone shows up in Finder like a USB drive. No app to open,
> no kernel extension, no developer mode, no payment — free and
> open source.

What it's *not*, said clearly:

- Not a sync tool. Not iCloud-for-Android. Not a backup app.
  Just file transfer.
- Not multi-protocol. USB only. No Wi-Fi, no SMB, no FTP.
- Not iPhones. macOS already handles iPhone sync.
- Not Linux or Windows. macOS only.
- Not a UI competitor to OpenMTP. We have no UI of our own
  beyond a menu bar icon; the UI is Finder.

The differentiator (shipped in v0.4.0):

> Comprador is the only Mac app that lets you mount two phones
> simultaneously in Finder. Plug in your phone and your
> partner's, drag files between them, treat them as
> independent volumes. Stream a video off either one without
> copying it across first.

The differentiator (after ImageCaptureCore lands, if it does):

> Comprador coexists with macOS's built-in Image Capture and
> Photos — no kernel extension, no system-service interference,
> sandbox-compatible. Apple's coordinator brokers the device;
> we join it instead of competing.

## What we will explicitly defer

These will come up. Have an answer prepared that says "yes, we
hear you, no, not for v1.0."

- **Sparkle-style auto-update.** Real feature, real ask. Not for
  v1.0. Stale-version risk is accepted; CVE-track cadence is
  documented in [SECURITY.md](SECURITY.md).
- **Intel Mac support.** Could happen. Not the priority. The
  cost is non-trivial because libmtp/libusb both need universal
  binaries and our CI runners would need to be x86_64-capable.
- **Older macOS (10.15, 11, 12) support.** Some users will ask.
  macOS 13 was chosen because of NFS pivot dependencies. We're
  not going to backport.
- **A native UI window like SwiftMTP.** Our UX bet is "Finder
  *is* the UI." If a user wants a window, they want a different
  product.
- **Cloud sync, backup, photo deduplication, AI features.**
  Adjacent product spaces. Not for Comprador.
- **Windows or Linux.** macOS only. Adjacent products may
  exist; that's fine.

## Launch checklist (go/no-go)

Items that gate the public launch announcement. Each is
specific and tickable.

### Hard requirements (no launch without)

- [x] App is notarized (v0.2.3 onward)
- [x] LICENSE is in the repo and the bundle (GPLv3-or-later)
- [x] NOTICES.md attributes all third-party code and assets
- [x] README explains "How It Works" in three steps
- [x] Logo (Merian/Sluyter, public domain)
- [x] **cgo callback buffer-reuse fix landed.** Removes the memory
      cliff; verified 2026-05-14 (the full-scale 9 GiB acceptance
      retest is an open verification follow-up). ([TODO.md](../TODO.md))
- [~] **Verified on at least one device class beyond the project's
      primary Sony Xperia 10 III.** v0.4.0 verified live on a
      **Pixel 6** (second vendor); ideally widen to three (a PTP
      camera, Samsung, or OnePlus).

### Soft requirements (launch with these noted as caveats)

- [ ] Multi-device implementation shipped *(or honest "coming
      in the next release" note)*
- [ ] Per-storage quota *(or honest caveat in README)*
- [ ] ImageCaptureCore tests run, results documented *(if
      tests pass, the pitch shifts and launch waits for the
      pivot; if they fail, ship without)*
- [x] Landing page live at
      **https://terraceonhigh.github.io/Comprador/**
      (source in [docs/site/](site/)); on-page SEO shipped per
      [SEO-PLAN.md](SEO-PLAN.md). Satellite how-to pages and
      off-page channel work (AlternativeTo, awesome-mac, repo
      topics) are tracked there, not here.
- [ ] README polished for non-developer reader (less jargon,
      more screenshots); swept for v0.4.0 (no stale WebDAV /
      helper / NFSv3 references)
- [ ] Screenshots / GIF in README that show the actual flow
      (plug in, tap, mount appears) — not just static frames

### Nice-to-have (do not gate)

- [ ] Sparkle auto-update infrastructure
- [ ] Telemetry-free crash reporting (e.g., a local-only crash
      log path)
- [ ] Translation of README to one other language (Portuguese
      feels apt given the name)
- [ ] A short demo video for the HN/Reddit post

## When to actually post

A post-on-Reddit-and-HN is a single shot in attention terms.
Wait until:

1. The "Hard requirements" above are all checked.
2. At least one **non-architect** has used the app end-to-end
   on a phone they own, without instructions, and not hit a
   blocker. (The architect can recruit a friend; this is the
   single highest-value test we haven't done yet.)
3. The known-issues list above is accurate as of the day of
   posting — re-verify each item the morning of.
4. The architect has a free afternoon to monitor the comment
   thread. Don't post and walk away; HN/Reddit threads need
   real-time replies for the first 6–8 hours.

## How this doc evolves

When an item lands or unlands, update its status. When the
ImageCaptureCore tests run, fold their results into the pitch
section. When a
real user reports something we hadn't anticipated, add it to
"Known issues."

The doc is not the launch announcement itself. The launch
announcement uses the pitch + known issues + the screenshot
demo. This doc is the underlying preparation; it should be
~2× the length of the actual post.
