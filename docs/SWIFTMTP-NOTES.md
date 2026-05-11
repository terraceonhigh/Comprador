# Notes on SwiftMTP

Forensic pass on [Neighbor-Z/SwiftMTP](https://github.com/Neighbor-Z/SwiftMTP)
(and its website source at
[Neighbor-Z/swiftmtp-website](https://github.com/Neighbor-Z/swiftmtp-website))
on 2026-05-09. Captured here so future us doesn't have to re-derive
it. Local clone lives at `~/Labs/SwiftMTP/`; the website source is
under `/tmp/swiftmtp-site/` if it's still there.

## What SwiftMTP is

A 2,557-line Swift app + a 6.1 MB Go dylib (`kalam.dylib`) called
via cgo through a 108-line C shim
([KalamShim/KalamShim.c](../../SwiftMTP/KalamShim/KalamShim.c)). The
"Kalam" backend is [`github.com/ganeshrvel/go-mtpx`](https://github.com/ganeshrvel/go-mtpx)
— same author as OpenMTP, the same backend OpenMTP itself uses,
repackaged as a dylib instead of driven from Electron via Node/N-API.

It is **not a Finder volume.** It's a SwiftUI window with its own
file browser. Drag-and-drop in and out of the window, Quick Look,
favorites, transfer progress bars — all inside its own UI. No mount,
no NetFS, no WebDAV, no NFS. The whole class of problems Comprador
has spent two weeks on does not exist for them because they did not
pick that fight.

## Comparison

| Axis | Comprador | SwiftMTP |
|---|---|---|
| Finder integration | Mount as Locations sidebar entry | None — own app window |
| MTP backend | `libmtp` (cgo) → `libusb` | `go-mtpx` (cgo) → `libusb` directly, **no libmtp** |
| Bridge process | Separate Go subprocess speaking NFS/HTTP on loopback | In-process Go dylib, JSON-over-cgo |
| ptpcamerad handling | Aggressive `killall` + 6-retry race | None — README tells the user to close Image Capture |
| Sandbox | Off | `ENABLE_APP_SANDBOX = YES` + `device.usb` + `disable-library-validation` (for bundled dylibs) |
| App size | ~7 MB DMG | <20 MB |
| macOS deployment | 13+ (NFS path needs it) | 12.0+ |
| Notarized | Yes, since v0.2.3 | No — README tells the user to `xattr -rd com.apple.quarantine` |
| Distribution | Standalone .dmg from GitHub Releases | Standalone .dmg from GitHub Releases |
| Languages | English only (README has a Chinese counterpart) | en, zh-Hans, zh-Hant, ja, es, ar |
| Multi-device | **Concurrent N (planned, gated on cgo fix)** — see [PLAN-MULTI-DEVICE.md](PLAN-MULTI-DEVICE.md) | N-detected, 1-active, switch-on-click (not concurrent) |

## On their "Multiple device connections" claim

Verified 2026-05-10 by direct source-reading
([KalamMTPManager.swift:1067–1096](../../SwiftMTP/SwiftMTP/Services/KalamMTPManager.swift)):
SwiftMTP's `switchDevice(to:)` does a **full `Dispose()` then
`Initialize()`** on each device click. The Go backend uses
go-mtpx's singleton `container.dev` ([structs.go:14](../../openmtp/ffi/kalam/native/structs.go)
— same backend OpenMTP uses), so only one device session can
exist at a time. The README's "Multiple device connections (v1.1)"
under "Realized" features is detection + UI switching, **not
concurrent session management.**

Comprador's planned multi-device is structurally different:
N subprocess bridges, N libmtp sessions, N NFS mounts. Both
references would need to fork or replace go-mtpx to match. The
moat is real.

## Things worth stealing

### 1. Direct-libusb PTP is a real alternative to libmtp

`go-mtpx` skips `libmtp` entirely and talks PTP over libusb itself.
The whole MISTAKES.md ledger we've built up — the
`LIBMTP_FILES_AND_FOLDERS_ROOT` constant gotcha, the
`Get_Folder_List_For_Storage` returning NULL on uncached devices,
`LIBMTP_destroy_file_t` double-free, the cgo callback per-call
allocation hitting 9 GiB on Attenborough — is libmtp-binding-shaped.
A future bridge rewrite onto go-mtpx would dodge that class entirely.
go-mtpx has its own different warts; we'd trade for them, not
eliminate them.

Migration cost: significant (rewrite the bridge's MTP layer). Not a
v0.3.3 thing. File as a v0.5+ "if we ever want to retire libmtp"
escape hatch.

### 2. Sandbox + USB-MTP is a working combination

We've been signing with `Comprador.debug.entitlements` (no sandbox)
and treating sandbox as out of scope. SwiftMTP demonstrates that:

```xml
<key>com.apple.security.app-sandbox</key>          <true/>
<key>com.apple.security.device.usb</key>           <true/>
<key>com.apple.security.cs.disable-library-validation</key> <true/>
```

…with bundled dylibs loaded via `@loader_path/`, plus the
`files.user-selected.read-write` entitlement for drag-and-drop
imports, is enough to ship sandboxed.

What this would cost us:

- `killall ptpcamerad` does not survive sandbox — that path would
  have to go.
- The bridge subprocess spawn probably *does* survive sandbox if the
  binary is bundled correctly.
- The NFS mount path is the open question — `mount(2)` invocations
  from a sandboxed process are restricted; we'd need to verify
  whether the user-mounted-NFS path we discovered (MISTAKES.md
  "SMAppService / Helper" §1) still works.

Not actionable now. File for "if MAS distribution ever becomes a
goal" — currently a non-goal per [CLAUDE.md](../CLAUDE.md) ("macOS
App Store distribution" listed under non-goals).

### 3. JSON-over-cgo as a Swift↔Go interface

Every operation in SwiftMTP:

1. Swift builds a JSON string (input parameters)
2. Swift calls a C function pointer in
   [`KalamShim.c`](../../SwiftMTP/KalamShim/KalamShim.c)
3. The Go dylib unmarshals JSON via jsoniter, performs the MTP work
4. Go calls back via three function-pointer callbacks (preprocess,
   progress, done) with a JSON-stringified result envelope
5. Swift parses the response JSON

They use a static `CallbackRouter` enum with `weak var manager`
because C function pointers can't capture Swift closures
([KalamMTPManager.swift:82](../../SwiftMTP/SwiftMTP/Services/KalamMTPManager.swift#L82)).
This is the canonical pattern for "Go dylib called from Swift" —
worth knowing if Comprador ever in-processes the bridge.

Tradeoffs against our subprocess+loopback model:

- **Their model:** lower latency, no port management, no
  orphan-process risk on eject, plays better with sandbox.
- **Our model:** cleaner crash isolation (bridge SIGSEGV doesn't
  take the menu bar down), clean process boundaries, easier to
  develop the bridge and the GUI independently.

Both work. The pick depends on whether bridge stability ever stops
being a concern; right now the subprocess boundary is load-bearing
for our debugging.

### 4. USB protocol-speed display

[USBMonitor.swift](../../SwiftMTP/SwiftMTP/Services/USBMonitor.swift)
walks IOKit to detect USB 2.0/3.0/3.1/3.2 and current negotiated
speed in Mbps. ~60 lines, no extra entitlements. Cheap UX win for
"why is my transfer slow" — we could surface "USB 2.0 (480 Mbps)"
in the menu bar's device-info section.

File for v0.4.x. Concretely: a small SF Symbols badge next to the
device name and a hover-tooltip with the speed string.

### 5. Single-page no-build static site for the marketing page

If we ever stand up `comprador.terrace.zone` or similar, the
SwiftMTP website is a usable template. Specifics:

- Pure HTML/CSS/JS, no framework, no build step. Six files total
  (index.html 24K, style.css 21K, i18n.js 32K, script.js 2.8K,
  README.md 108B, plus an `assets/` directory with light + dark
  versions of the icon and a screenshot).
- CSS-variable-driven theming: `[data-theme="light"]` /
  `[data-theme="dark"]` swaps the whole palette. Persisted in
  `localStorage['swiftmtp-theme']`.
- i18n via `data-i18n="key"` attributes on every translatable
  element + a flat `i18n[lang][key]` object literal. Six languages
  including Arabic. ~80 keys cover the whole page.
- `IntersectionObserver`-based reveal-on-scroll with stagger.
- Two-tone screenshots (`screenshot.png` + `screenshot-dark.png`)
  swapped by JS when the theme toggles.
- No analytics. Only external dep is Google Fonts (Inter 300–800).
- Hosted on GitHub Pages, served from `main` branch root.

Total source: <100 KB. Achievable in an afternoon if and when the
"public landing page" question becomes worth answering. Currently
not on any roadmap; logging because it's a small, copyable artifact.

### 6. Pseudonymized comparison-table convention

SwiftMTP's comparison table lists competitors as "Open Source A",
"Open Source B", "App Store A/B/C" rather than naming OpenMTP /
Macroplant / Android File Transfer directly
([index.html:200–305](https://github.com/Neighbor-Z/swiftmtp-website/blob/main/index.html)).
Sidesteps trademark and defamation risk while still making the pitch.

If Comprador ever does a public comparison (in README, on a website,
or in a release-notes comparison post), this convention is cheap
discipline to copy.

## Things to *not* steal

### `lockMtp()` is a no-op masquerading as a mutex

[helpers.go:191](../../SwiftMTP/ffi/kalam/native/helpers.go#L191):

```go
func lockMtp() error {
    if container.locked {
        return fmt.Errorf("ErrorMtpLockExists")
    }
    container.locked = true
    defer func() {
        container.locked = false
    }()
    return nil
}
```

`defer` runs when `lockMtp` returns, not when the *caller* returns.
So the lock is set-and-immediately-released; the check is racy. They
don't hit it in practice because Swift drives strictly-serial calls
via the `operation` enum on the Swift side.

The pattern (caller-serializes) is fine; their implementation of it
is a bug. Our session-goroutine model in
[bridge/mtp/session.go](../../bridge/mtp/session.go) achieves the
same property correctly.

### Simple hotplug retry without `pendingAttach` queue

SwiftMTP retries USB connect attempts with a 2-second delay and a
max retry count
([KalamMTPManager.swift:556+](../../SwiftMTP/SwiftMTP/Services/KalamMTPManager.swift#L556)).
No queueing of attach events that arrive while a previous teardown
is still in flight. They don't need it because they don't unmount —
but if they ever did, they'd re-walk the path we already walked
([MISTAKES.md entry 19a](MISTAKES.md)).

We keep the `pendingAttach` queue. They don't have it because their
problem is smaller, not because they solved it differently.

### "We're smaller than OpenMTP" as a headline frame

Their entire marketing frame is "we're 20 MB instead of 360 MB."
Comprador is small too (the v0.3.2 .dmg is ~7 MB), but leaning into
"smaller" as the headline frames the product as "OpenMTP minus
weight." That's not Comprador's pitch. Our pitch is "phone in
Finder, no install ceremony" — a different value proposition that
needs a different headline. Don't accidentally inherit their frame
when writing copy.

### "It's not signed because Apple charges $99" register

SwiftMTP's README and FAQ make a small political point about
notarization
([README.md:134–140](https://github.com/Neighbor-Z/SwiftMTP/blob/main/README.md)).
We chose differently; v0.2.3 was the first notarized release and
that's now the baseline. The first-launch-friction reduction is
real. Don't drift back toward "tell the user to xattr away the
quarantine" thinking just because it's lower-effort — we already
paid the cost; the payoff is in every double-click.

## The fork that matters

The lesson isn't "we picked wrong." It's that **SwiftMTP solved the
integration problem by deciding not to integrate.** Every NFS pivot,
WebDAV writeseq investigation, helper-detour, AMFI rejection — all
of it is the cost of insisting that the phone show up in Finder.
SwiftMTP's existence at <20 MB, sandbox-clean, with no kernel-mount
machinery, is a useful counter-existence-proof that confirms our
chosen difficulty is real difficulty, deliberately taken on.

If we ever want a back-pocket "we could pivot to a native window"
architecture, SwiftMTP is the worked example. The expensive thing
we have that they don't is the Finder integration; the expensive
thing they have that we don't is a UI. Both real. Which one the
user actually wants is the product question, not an engineering
one — see [docs/USER.md](USER.md).

## Receipts

Local clone: `~/Labs/SwiftMTP/`. Last fetched 2026-05-09.

Files most worth re-reading if the question comes up again:

- [SwiftMTP/Services/KalamMTPManager.swift](../../SwiftMTP/SwiftMTP/Services/KalamMTPManager.swift) — 1,267 lines, the Swift↔Go callback machinery
- [SwiftMTP/Services/USBMonitor.swift](../../SwiftMTP/SwiftMTP/Services/USBMonitor.swift) — 566 lines, the USB-protocol-speed detection
- [KalamShim/KalamShim.c](../../SwiftMTP/KalamShim/KalamShim.c) — 69 lines, the C bridging
- [ffi/kalam/native/kalam.go](../../SwiftMTP/ffi/kalam/native/kalam.go) — 481 lines, the Go-side `//export` surface
- [SwiftMTP.entitlements](../../SwiftMTP/SwiftMTP/SwiftMTP.entitlements) — the sandbox-compatible entitlement set
- [Comparison.md](../../SwiftMTP/Comparison.md) — their pitch in compressed form
