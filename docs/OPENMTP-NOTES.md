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
master at some early point. The ancestry is **historical only** —
no source code remains:

- **The `openmtp` clone in `~/Labs/` is reference material only.**
  CLAUDE.md is explicit: "Don't modify." We pull from it when we
  need to remember how the upstream solved a particular MTP edge
  case, not to merge upstream changes.
- **No OpenMTP code remains in Comprador.** Per
  [NOTICES.md](../NOTICES.md), commit `402f147` ("Remove all
  legacy OpenMTP code") cleared it out. The bridge was rewritten
  in Go around `libmtp` (different choice from upstream's
  `go-mtpx`), and the Swift menu bar app is a clean
  reimplementation.
- **The fork relationship was formally severed on GitHub.**
  Comprador no longer carries OpenMTP's MIT copyright.
- **Comprador's current license is GPLv3-or-later** ([LICENSE](../LICENSE)),
  not MIT. The relicense happened with the OpenMTP-code removal,
  since MIT inheritance no longer applied once the inherited code
  was gone. NOTICES.md preserves the historical acknowledgement.
- **What is preserved is the README ancestry note**
  ([README.md:187](../README.md)) — a courtesy beyond what GPL or
  CC-BY would require.

## Architectural divergence at a glance

| Axis | OpenMTP | Comprador |
|---|---|---|
| Shell | Electron (Chromium + Node) | Native Swift menu bar |
| Backend | Go dylib (`go-mtpx`), in-process | Go subprocess (`libmtp`-based), separate |
| Surface | Own app window | Finder volume mount |
| Size | ~360 MB | ~7 MB |
| Notarized | Yes | Yes (since v0.2.3) |
| Multi-device | **No** — rejects with `ErrorMultipleDevice` | **Concurrent N (planned, gated on cgo fix)** — see [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md) |
| Integration ceremony | Open the app | Plug in, tap File Transfer |

## On the "Multi-device: Yes" line that used to live here

An earlier version of this comparison table said OpenMTP supported
multi-device. **It doesn't.** Verified 2026-05-10 by direct
source-reading: OpenMTP's `Initialize()` returns
`ErrorMultipleDevice` ("Multiple MTP devices found") if more than
one MTP device is attached
([send_to_js/helpers.go](../../references/openmtp/ffi/kalam/native/send_to_js/helpers.go),
user-facing message in
[processBufferOutput.js](../../references/openmtp/app/helpers/processBufferOutput.js)).
The user has to physically disconnect extras before OpenMTP works
at all. The backend uses a singleton `container.dev`
([structs.go:13–17, kalam.go:19](../../references/openmtp/ffi/kalam/native/structs.go))
and provides no path to N sessions.

This makes Comprador's planned concurrent multi-device a genuine
differentiator — the architectural cost is one OpenMTP can't pay
without forking or replacing go-mtpx.

## What's still useful as reference

OpenMTP's source is the canonical implementation of "MTP-on-macOS
that someone actually ships." When we hit an MTP corner that libmtp
doesn't handle gracefully and we suspect `go-mtpx` does it
differently, this is where we look:

- [`ffi/kalam/`](../../references/openmtp/ffi/kalam/) — the cgo Go MTP backend.
  Same codebase that SwiftMTP also uses. Read this when our libmtp
  binding hits an edge case (e.g. the `LIBMTP_FILES_AND_FOLDERS_ROOT`
  constant gotcha, MISTAKES.md §2 — `go-mtpx`'s equivalent walks the
  flat handle space differently).
- [`app/main.dev.js`](../../references/openmtp/app/main.dev.js) — Electron main
  process and IPC plumbing. Useful only as a "this is what the
  Electron-wrapped pattern looks like." Comprador doesn't use this.
- [`app/containers/HomePage/`](../../references/openmtp/app/containers/HomePage)
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
"OpenMTP plus Finder integration" — we share lineage but not
license, code, or product shape. We don't track upstream. We
don't backport.

What we owe upstream:

- Historical attribution in README and NOTICES.md (already done)
- An honest distinguishing position when describing the project in
  any public forum

What we don't owe upstream (since the fork was severed and no
OpenMTP code remains):

- License continuity (Comprador is GPLv3-or-later; OpenMTP is MIT)
- Engineering compatibility
- Backporting bug fixes
- Naming continuity
- Adopting their architectural decisions

## Receipts

- README ancestry note: [README.md:187](../README.md)
- Comprador's current license (GPLv3-or-later, not MIT): [LICENSE](../LICENSE)
- The fork-severance record: [NOTICES.md](../NOTICES.md) "Historical: OpenMTP" section
- The reason "Why not just OpenMTP?" framing exists in
  [CLAUDE.md](../CLAUDE.md) project intro and in
  [USER.md](USER.md) (the user who tried OpenMTP and bounced)
- The `go-mtpx` vs `libmtp` tradeoff captured in
  [SWIFTMTP-NOTES.md](SWIFTMTP-NOTES.md) §"Things to steal" item 1
