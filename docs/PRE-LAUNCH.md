# Pre-launch readiness — Reddit / Hacker News

> **⚠ Stale baseline (2026-06-08).** The substance below predates the
> **Galatea substrate swap** (v0.4.0): Comprador now serves NFSv4, not WebDAV
> or willscott-NFSv3, and read / write / full file management all work live.
> The current honest status and known limitations are the v0.4.0 entry in
> [CHANGELOG.md](../CHANGELOG.md); the pitch and known-issues sections here need
> a refresh against that before any announcement — a with-the-Architect task,
> since it's a product/messaging call. Treat the specifics below as v0.3.2-era.

**Status — 2026-05-10:** Comprador is at v0.3.2, working, not
yet publicly announced. This doc tracks the path from "shipping
quietly to early users" to "ready for a Reddit / HN post." Both
the things we have today and the things contingent on the next
two-to-four weeks of work.

This doc gets updated as the launch approaches. The structure
below is the durable shape; the contents change.

## What we'd be announcing today (v0.3.2 baseline)

A working macOS app that mounts Android phones (and PTP/MTP
cameras) as Finder volumes. Plug in, tap File Transfer, the
phone appears in the Locations sidebar. ~7 MB notarized DMG,
macOS 13+, Apple Silicon only.

### Defensible claims today

- **Phone-in-Finder, plug-and-play.** No installer ceremony
  beyond drag-to-Applications. No Developer Options on the
  phone. No kernel extension. No SIP disable. No subscription.
  No account.
- **Notarized.** First launch is just a double-click; no
  right-click → Open dance.
- **No telemetry, no phone-home.** The bridge binds to
  loopback. No outbound network connections. Explicit in
  [SECURITY.md](SECURITY.md).
- **Native menu bar app.** ~7 MB DMG, not Electron, not a web
  view, not a 360 MB shell.
- **NFSv3 mount, sub-second mount time.** No 90-second WebDAV
  preflight wait. The mount feels like a USB stick.
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
| Is it safe? | Threat model at [SECURITY.md](SECURITY.md). Loopback-only NFS, no telemetry, library validation disabled (for bundled dylibs), USB entitlement scoped to known MTP vendors, privileged helper bundled but not on the mount path (slated for removal in v0.4.0). |
| Why's it called Comprador? | Portuguese-origin term for the native intermediary in Western trading firms operating in colonial-era Macau and Canton. The project takes its visual palette from Iberian/Macanese vernacular (the logo is a 1705 Merian engraving — see [NOTICES.md "Logo"](../NOTICES.md)). |

### Known issues we will disclose

Be honest. These are the things people will hit. Disclose
proactively rather than letting comment threads surface them.

- **First-plug-after-app-start may fail.** If a phone is
  already connected when Comprador launches, our bridge may
  not win the USB-interface claim race against macOS's
  `ptpcamerad`. The app shows a notification: unplug and
  replug. We hope to remove this entirely if the
  [ImageCaptureCore research](RESEARCH-IMAGECAPTURECORE.md)
  pays off.
- **Apple Silicon only, macOS 13+.** Intel Mac and older macOS
  are not supported. Intel is not on the roadmap; older macOS
  may be a future ask.
- **Single device at a time** *(currently)*. Concurrent
  multi-device is the next major feature. Plan in
  [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md). Gated on the
  cgo callback buffer-reuse fix being in [TODO.md
  "Roadmap imperative"](../TODO.md).
- **Per-storage quota is aggregated** *(currently)*. Phones
  with SD cards report aggregate free space to Finder, which
  can mislead Finder's "X GB available" preflight. Fix planned
  in [PLAN-MULTI-STORAGE.md](PLAN-MULTI-STORAGE.md).
- **Memory cliff on 8 GiB Macs with multi-GiB transfers.** Per
  [MISTAKES.md entry 8a](MISTAKES.md), the cgo callback path
  leaks ~one-file-size's worth of `VM_ALLOCATE` regions per
  transfer until the bridge dies. Fix is ~30 lines of Go;
  [TODO.md "Roadmap imperative"](../TODO.md). On 16 GiB+ Macs
  this is rarely user-visible.
- **No auto-update.** Manual DMG download from GitHub Releases.
  Trade-off: also no silent supply-chain attack vector.
  Sparkle-style auto-update is on the longer-term roadmap.

## What we'd be announcing in 2–4 weeks

Contingent on three pieces of work landing in roughly the order
listed. Each is bounded; none requires open-ended research.

### Definite (with the cgo fix)

The cgo callback buffer-reuse fix is ~30 lines of Go. It
unlocks:

- **Multi-GiB transfers stop leaking memory.** Bridge process
  footprint stays bounded regardless of file size.
- **Concurrent multi-device becomes shippable.** Two phones
  plugged in, two Finder Locations sidebar entries, both
  browseable in parallel. This is the genuinely-unique feature
  in this niche — verified by source-reading of OpenMTP and
  SwiftMTP that neither does this concurrently.

### Plausible (with the multi-storage implementation)

Per-storage `statfs(2)`. Bridge already has the data layer;
fix is a small go-nfs patch and adapter routing. ~1 day.

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
- **Privileged helper comes out faster.** Already slated for
  v0.4.0 removal.

If they fail, nothing changes architecturally — we keep
libmtp + seizure + helper + dext-on-roadmap. The doc becomes a
closed investigation.

## The pitch

A two-sentence version for the post title and first paragraph:

> Comprador mounts your Android phone (or PTP camera) as a
> Finder volume. Plug in, tap File Transfer, drag files. No
> install ceremony, no developer mode, no kernel extension, no
> Electron, no telemetry, no subscription.

What it's *not*, said clearly:

- Not a sync tool. Not iCloud-for-Android. Not a backup app.
  Just file transfer.
- Not multi-protocol. USB only. No Wi-Fi, no SMB, no FTP.
- Not iPhones. macOS already handles iPhone sync.
- Not Linux or Windows. macOS only.
- Not a UI competitor to OpenMTP. We have no UI of our own
  beyond a menu bar icon; the UI is Finder.

The differentiator (after the cgo fix lands):

> Comprador is the only Mac app that lets you mount two phones
> simultaneously in Finder. Plug in your phone and your
> partner's, drag files between them, treat them as
> independent volumes.

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
- [ ] **cgo callback buffer-reuse fix landed.** Removes the memory
      cliff. ([TODO.md](../TODO.md))
- [ ] **Verified on at least one device class beyond the project's
      primary Sony Xperia 10 III.** Camera (any PTP), Pixel,
      Samsung, OnePlus. Ideally three.

### Soft requirements (launch with these noted as caveats)

- [ ] Multi-device implementation shipped *(or honest "coming
      in the next release" note)*
- [ ] Per-storage quota *(or honest caveat in README)*
- [ ] ImageCaptureCore tests run, results documented *(if
      tests pass, the pitch shifts and launch waits for the
      pivot; if they fail, ship without)*
- [ ] README polished for non-developer reader (less jargon,
      more screenshots)
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
section. When the cgo fix ships, update the differentiator
language. When a real user reports something we hadn't
anticipated, add it to "Known issues."

The doc is not the launch announcement itself. The launch
announcement uses the pitch + known issues + the screenshot
demo. This doc is the underlying preparation; it should be
~2× the length of the actual post.
