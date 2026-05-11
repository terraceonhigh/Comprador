# Notes on OpenMTP

The repo Comprador forked from. Local clone at `~/Labs/openmtp/`,
last fetched showing upstream HEAD `ac02705` from 2025-08-13. License
MIT (Ganesh Rathinavel). 144 MB on disk; ~171 JS files, ~11 Go files
in the Kalam backend.

## What OpenMTP is

A macOS Android-File-Transfer replacement: 360 MB Electron app
wrapping an in-process Go-cgo MTP backend ("Kalam Kernel" =
[ganeshrvel/go-mtpx](https://github.com/ganeshrvel/go-mtpx)). The
user opens OpenMTP.app, sees a split-pane file manager (local on
left, Android device on right), drags files between panes. Not a
Finder volume — its own UI.

Originally featured a "Legacy" mode using a libmtp-style backend
for older macOS; the Kalam path replaced it on Big Sur+ and is
~10–100× faster. OpenMTP's competitive frame for years was "Android
File Transfer is buggy and limited; we're free and fast." When
Google killed Android File Transfer in late 2024, OpenMTP became
the de-facto answer for Mac+Android file transfer — but at 360 MB
with a web-feeling UI, lots of users tried it and bounced (see
[USER.md](USER.md): "tried OpenMTP, briefly").

## What Comprador inherited

Comprador's `master` branch is descended from a fork of OpenMTP's
master at some early point. The ancestry is mostly historical now:

- **The `openmtp` clone in `~/Labs/` is reference material only.**
  CLAUDE.md is explicit: "Don't modify." We pull from it when we
  need to remember how the upstream solved a particular MTP edge
  case, not to merge upstream changes.
- **No code is left in Comprador's `bridge/` that traces directly
  to OpenMTP's main repo.** The bridge was rewritten in Go around
  `libmtp` (different choice from upstream's `go-mtpx`), and the
  Swift menu bar app is a clean reimplementation.
- **What's preserved is the README ancestry note**
  ([README.md:187](../README.md)) and the LICENSE — we inherited
  MIT from upstream and stay MIT.

## Architectural divergence at a glance

| Axis | OpenMTP | Comprador |
|---|---|---|
| Shell | Electron (Chromium + Node) | Native Swift menu bar |
| Backend | Go dylib (`go-mtpx`), in-process | Go subprocess (`libmtp`-based), separate |
| Surface | Own app window | Finder volume mount |
| Size | ~360 MB | ~7 MB |
| Notarized | Yes | Yes (since v0.2.3) |
| Multi-device | Yes | One device at a time |
| Integration ceremony | Open the app | Plug in, tap File Transfer |

## What's still useful as reference

OpenMTP's source is the canonical implementation of "MTP-on-macOS
that someone actually ships." When we hit an MTP corner that libmtp
doesn't handle gracefully and we suspect `go-mtpx` does it
differently, this is where we look:

- [`ffi/kalam/`](../../openmtp/ffi/kalam/) — the cgo Go MTP backend.
  Same codebase that SwiftMTP also uses. Read this when our libmtp
  binding hits an edge case (e.g. the `LIBMTP_FILES_AND_FOLDERS_ROOT`
  constant gotcha, MISTAKES.md §2 — `go-mtpx`'s equivalent walks the
  flat handle space differently).
- [`app/main.dev.js`](../../openmtp/app/main.dev.js) — Electron main
  process and IPC plumbing. Useful only as a "this is what the
  Electron-wrapped pattern looks like." Comprador doesn't use this.
- [`app/containers/HomePage/`](../../openmtp/app/containers/HomePage)
  (likely path) — the file-manager UI logic. Not relevant to us
  since we don't have a UI; potentially useful if we ever need
  reference for "how did OpenMTP handle MTP errors in user-visible
  ways" — they will have prior art there.

## What we deliberately *didn't* keep from upstream

- **Electron.** Rejected as the framework choice on day one of
  Comprador. Electron's footprint, RAM use, and "web feel" are the
  things that drive users away from OpenMTP, and the bridge layer
  was the only thing we needed.
- **Web-tech UI.** No HTML/CSS/JS in Comprador. SwiftUI menu bar +
  Finder integration only.
- **Two-pane file manager.** We deliberately have no UI of our own.
  Files appear in Finder; that's the entire surface.
- **Sentry telemetry.** OpenMTP wires Sentry into both the renderer
  and the main process for error reporting. Comprador doesn't phone
  home; this is a deliberate value choice (see [USER.md](USER.md):
  "no signed-in account, no subscription").

## What about the Kalam backend itself?

We chose `libmtp` over `go-mtpx`. The reasoning, recovered from
context:

- **`libmtp` is older, more battle-tested, has more device support
  baked in.** When a phone-vendor edge case surfaces, libmtp
  usually has someone's prior fix in tree.
- **`go-mtpx` is one author's project, less vetted, but cleaner
  Go-native code.** We could read it; we couldn't read libmtp's
  full C source profitably in the same time budget.
- **Migrating to `go-mtpx` is an option** if libmtp ever becomes
  the binding constraint. See [SWIFTMTP-NOTES.md](SWIFTMTP-NOTES.md)
  §"Things to steal" item 1 for that escape hatch's full shape.

The trade is real. `go-mtpx` is the "what ships in OpenMTP and
SwiftMTP" answer; libmtp is the "what every Linux distro ships
that talks to phones" answer. Neither is wrong; we chose the older,
broader one and have absorbed its costs in MISTAKES.md.

## Things to steal (if any)

The honest answer: **almost nothing.** Most of OpenMTP's choices are
ones we've intentionally rejected. The exceptions are minor:

- **The "Kalam" backend choice as a future option** — already
  filed via SWIFTMTP-NOTES.md.
- **Their device vendor-ID list** — `~/Labs/openmtp/app/data/`
  (likely path) probably has a curated list. Cross-check with our
  `MenuBarApp/Resources/VendorIDs.plist` next time we add a vendor;
  we shouldn't ship narrower coverage than upstream when the
  difference is just a missing line in our list.
- **The `ffi/kalam/` README** if you ever consider building Kalam
  yourself — they document the cross-compile dance for arm64 and
  x86_64 dylibs.

## Things to *not* steal

- **The Electron wrapper.** Rejected on day one.
- **The split-pane UI.** Rejected by the entire premise of
  Comprador.
- **The "Kalam Kernel" branding.** Specific to OpenMTP's marketing;
  we have no parallel. If we use go-mtpx in the future, we'd just
  call it the bridge's MTP layer.
- **The RTL marketing voice** ("Countless searches to find an app to
  solve these problems and failing to find one made me restless. So,
  I took the leap..."). Their README is heartfelt and verbose;
  ours is technical and terse. See [USER.md](USER.md) on register.

## The fork relationship, going forward

OpenMTP is upstream-active (last commit Aug 2025). They will
continue to ship. Comprador is no longer in any meaningful sense
"OpenMTP plus Finder integration" — we share lineage and license,
but the shape of the product, the backend, and the integration
strategy are all ours. We don't track upstream. We don't backport.

What we owe upstream:

- Attribution in README and LICENSE (already done)
- An honest distinguishing position when describing the project in
  any public forum

What we don't owe upstream:

- Engineering compatibility
- Backporting bug fixes
- Naming continuity beyond LICENSE
- Adopting their architectural decisions

## Receipts

- README ancestry note: [README.md:187](../README.md)
- License inheritance: [LICENSE](../LICENSE) (root of Comprador)
- The reason "Why not just OpenMTP?" framing exists in
  [CLAUDE.md](../CLAUDE.md) project intro and in
  [USER.md](USER.md) (the user who tried OpenMTP and bounced)
- The `go-mtpx` vs `libmtp` tradeoff captured in
  [SWIFTMTP-NOTES.md](SWIFTMTP-NOTES.md) §"Things to steal" item 1
