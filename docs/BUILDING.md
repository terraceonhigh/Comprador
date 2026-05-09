# Building Comprador

## Prerequisites

### Required software

```bash
# Go 1.21+
/opt/homebrew/bin/go version

# libmtp (via Homebrew)
brew install libmtp

# Verify libmtp works with a connected phone
mtp-detect    # should show device info
```

### Swift toolchain

You can build with either:

- **Full Xcode** — required for the `make app`, `make app-debug`,
  `make run`, and `make dist` targets:

  ```bash
  xcode-select -p    # must point to Xcode.app, not CommandLineTools
  sudo xcode-select -s /Applications/Xcode.app
  sudo xcodebuild -license accept
  sudo xcodebuild -runFirstLaunch
  ```

- **Command Line Tools only** — `make app-swiftc`, `make run-swiftc`,
  `make dist-swiftc` skip Xcode entirely and drive the build with
  `swiftc` directly. No license accept, no DerivedData. Faster
  iteration if you don't need Xcode for anything else.

### Required hardware

A physical Android device connected via USB with **File Transfer** mode
selected. A data-capable USB cable (not charge-only).

### libmtp header

The cgo bindings need the libmtp header. It's already checked into
`bridge/cvendor/libmtp.h`. If your libmtp version differs:

```bash
cp $(brew --prefix libmtp)/include/libmtp.h bridge/cvendor/libmtp.h
```

## NFS pivot dependency setup

Before building `nfs-stub` or any NFS-path code, the bridge module needs
go-nfs and its transitive dependencies resolved. Run once after pulling:

```bash
cd bridge && go mod tidy
```

This downloads `github.com/willscott/go-nfs` and all its dependencies
(go-billy, uuid, lru, go-xdr, etc.) and populates `go.sum`. Requires
internet access and a working Go toolchain.

## Build Targets

```bash
# Just the Go bridge binary
make bridge

# Just the privileged helper binary
make helper

# Run the helper unit tests
make helper-test

# Phase 1 NFS pivot verification stub (memfs, no MTP, no phone needed)
make nfs-stub

# Run the Go bridge standalone (for manual WebDAV testing)
make dev

# --- Xcode-based targets ---

# Swift app (Debug) with bridge bundled
make app-debug

# Build and run the app (kills existing instance)
make run

# Distributable Release .app + .zip
make dist

# --- swiftc-based targets (no Xcode required) ---

# Build the .app via swiftc directly
make app-swiftc

# Build and run via swiftc
make run-swiftc

# Distributable .app + .zip via swiftc
make dist-swiftc

# Clean all build artifacts
make clean
```

## What `make dist` produces

```
dist/
├── Comprador.app/
│   ├── Contents/
│   │   ├── MacOS/
│   │   │   ├── Comprador                              # Swift menu bar app
│   │   │   └── comprador-helper                       # Privileged helper
│   │   ├── Library/
│   │   │   └── LaunchDaemons/
│   │   │       └── com.comprador.helper.plist         # SMAppService.daemon
│   │   ├── Resources/
│   │   │   ├── bridge                                 # Go WebDAV bridge
│   │   │   └── VendorIDs.plist                        # Android vendor IDs
│   │   ├── Frameworks/
│   │   │   ├── libmtp.9.dylib                         # MTP library
│   │   │   └── libusb-1.0.0.dylib                     # USB library
│   │   └── Info.plist
│   └── ...
└── Comprador.zip                                       # ~5MB, ready to share
```

All dynamic library paths are rewritten with `install_name_tool` to use
`@executable_path/../Frameworks/`, so the app is self-contained. No
Homebrew installation needed on the target Mac.

The helper plist's `BundleProgram` points at
`Contents/MacOS/comprador-helper`; `SMAppService.daemon(plistName:)`
reads it from the bundle on first registration. The daemon stays
disabled until the user approves it under System Settings → Login Items.

## Development Workflow

### Testing the NFS stub (Phase 1 verification)

This proves macOS mounts go-nfs without the ~90s WebDAV quota wait.
Requires sudo and no phone needed.

```bash
# 1. Resolve dependencies (once)
cd bridge && go mod tidy && cd ..

# 2. Build and run the stub
make nfs-stub
./build/nfsstub
# Prints: PORT=XXXXX plus a ready-to-paste mount command

# 3. In a second terminal — mount (requires sudo):
mkdir -p /tmp/nfsstub
sudo mount -o port=XXXXX,mountport=XXXXX,nfsvers=3,nolocks,tcp -t nfs localhost:/ /tmp/nfsstub

# 4. Verify: mount should return in <5s. Open Finder — volume appears.
#    Files: hello.txt and Photos/readme.txt should be visible.
#    Try dragging hello.txt to Desktop to verify read works.

# 5. Unmount when done
sudo umount /tmp/nfsstub
```

**Pass criterion:** mount completes in under 5 seconds with no 90s stall.
If this passes, the NFS pivot is safe to build on. If it fails or hangs,
stop and document the failure in docs/MISTAKES.md before proceeding.

### Testing the bridge standalone

```bash
make dev
# Bridge prints PORT=XXXXX
# In Finder: Go → Connect to Server → dav://localhost:XXXXX
```

### Testing the full app

```bash
make run
# App appears in menu bar
# Plug in phone, select File Transfer
# Phone appears as volume in Finder
```

### Running the integration test suite

```bash
# Requires phone connected in File Transfer mode
./test.sh
```

## Platform Notes

- **ARM only**: The bridge binary and bundled dylibs are arm64. No x86_64
  (Intel Mac) support in the current build config. To add it, build the
  bridge as a universal binary and bundle both architectures of the dylibs.
- **macOS 13+**: The Swift app uses `IOUSBHostDevice` matching (introduced
  in macOS 13). Older macOS would need `IOUSBDevice` matching.
- **Not notarized**: The app is ad-hoc signed. First launch requires
  right-click → Open to bypass Gatekeeper. Notarization requires an
  Apple Developer account ($99/year).

## The build chain that shipped v0.3.0

Recorded for future-us to deliberate on which targets are still earning
their keep. Captured 2026-05-09 after the helper-free NFS architecture
was verified end-to-end on `gala`.

### One command, what it does

A single invocation produces the install-ready bundle:

```bash
make app-signed
```

Resolved in dependency order, this is what runs:

1. **`make bridge`** — `cd bridge && CGO_CFLAGS=... CGO_LDFLAGS="-L/opt/homebrew/lib"
   go build -ldflags="-X main.BuildID=<git SHA>" -o build/bridge .`
   Produces the Go bridge binary, ~10 MB, linked against Homebrew's
   libmtp/libusb at their `/opt/homebrew/...` install paths.

2. **`make helper`** — `cd helper && go build -o build/comprador-helper .`
   Produces the privileged-helper binary. **Vestigial after the
   2026-05-08 refactor; the NFS path no longer invokes it.** It still
   gets bundled into the .app for legacy WebDAV cosmetics (`/etc/hosts`
   hostname rename) and to keep the bundle layout stable across the
   v0.2.x → v0.3.0 transition. **Cull candidate** for v0.4.0 once the
   WebDAV path is fully retired.

3. **`make app-swiftc`** — pure-`swiftc` Swift app build (no Xcode), then
   bundle assembly:
   - Generate `build/BuildInfo.swift` with the BUILD_ID baked in
   - `swiftc` the Swift sources → `build/swift/Comprador`
   - Create `build/swift/Comprador.app/Contents/{MacOS,Resources,Frameworks,Library/LaunchDaemons}`
   - Copy the Swift binary, `Info.plist`, `VendorIDs.plist`, write `PkgInfo`
   - **`BUNDLE_BRIDGE` macro:** copy `build/bridge` →
     `Contents/Resources/bridge`, copy libmtp/libusb dylibs →
     `Contents/Frameworks/`, `install_name_tool -change` to rewrite
     load-paths from `/opt/homebrew/...` to
     `@executable_path/../Frameworks/...`, then `codesign --force --sign -`
     (ad-hoc) each binary. **The libusb `install_name_tool -change` on
     the bridge itself was added 2026-05-08 after a hardened-runtime
     cdhash mismatch broke loading; without it, the bundled libusb is
     rejected at dlopen time on Developer-ID-signed builds.**
   - **`BUNDLE_HELPER` macro:** copy `build/comprador-helper` →
     `Contents/MacOS/comprador-helper`, copy
     `helper/com.comprador.helper.plist` →
     `Contents/Library/LaunchDaemons/`, ad-hoc sign. **Also cull
     candidate** alongside the helper binary itself.
   - Final `codesign --force --deep --sign - --entitlements
     MenuBarApp/Comprador.debug.entitlements --options runtime` on the
     bundle. Debug entitlements omit
     `com.apple.developer.system-extension.install` (which would require
     a provisioning profile).

4. **`make dist-swiftc`** — `rm -rf dist/`, copy
   `build/swift/Comprador.app` to `dist/Comprador.app`, zip into
   `dist/Comprador.zip`. The zip is only used by the `notarytool`
   submission flow; for local install the `.app` directory in `dist/`
   is what we install.

5. **`make app-signed`** — re-signs everything in `dist/Comprador.app`
   with the local Developer ID Application certificate. Ordering is
   deepest-first per Apple's guidance:
   - `codesign --force --options runtime --timestamp --sign "Developer ID..."`
     on each of `Frameworks/libmtp.9.dylib`,
     `Frameworks/libusb-1.0.0.dylib`, `Resources/bridge`,
     `MacOS/comprador-helper`
   - Then `codesign ... --entitlements Comprador.debug.entitlements ...
     <bundle>`
   - Verify: `codesign --verify --strict --verbose=2 <bundle>`

   `--timestamp` requires network access to Apple's timestamp authority.

### Install over the existing `/Applications/Comprador.app`

```bash
killall Comprador 2>/dev/null   # quit any running instance
umount "$HOME/Library/Application Support/Comprador/Volumes/"*  # release any active NFS mount
rm -rf /Applications/Comprador.app
cp -R dist/Comprador.app /Applications/Comprador.app
```

No `sudo` required — `/Applications` is user-writable for app installs
in standard macOS configurations.

### Launch (CLI for stderr capture, useful during dev)

```bash
/Applications/Comprador.app/Contents/MacOS/Comprador
```

Or normal-launch by clicking the app in Finder / Spotlight.

### Optional: notarize before installing

If a fresh install on a *different* Mac is needed and Gatekeeper-clean
is required, run `make app-notarized` instead of `make app-signed`. This
appends:

6. `ditto -c -k --keepParent dist/Comprador.app dist/Comprador.zip`
7. `xcrun notarytool submit dist/Comprador.zip --keychain-profile
   notarytool-password --wait` (3–5 minute wall clock, depends on Apple
   notary queue)
8. `xcrun stapler staple dist/Comprador.app`
9. `xcrun stapler validate dist/Comprador.app`

Requires a one-time `xcrun notarytool store-credentials
notarytool-password --apple-id <id> --team-id <team>` setup.
**Empirically (2026-05-08) notarization is *not* required for the
SMAppService daemon path on macOS Sequoia — the helper-launch issue we
spent six hours diagnosing turned out to be unrelated. Notarization
remains the right answer for distribution-grade builds (Gatekeeper
trust on fresh machines), but is not load-bearing for
local-development trust.**

### Summary: targets currently in use vs vestigial

| Target | Used in v0.3.0 ship path? | Notes |
|---|---|---|
| `bridge` | ✓ | Core Go binary |
| `helper` | partial | Bundled but not invoked on NFS path; legacy WebDAV cosmetic only |
| `helper-test` | ✓ (CI) | Unit tests for the helper RPC protocol |
| `nfs-stub` | × | Phase 1 verification artifact; superseded; cull candidate |
| `app` | × | Xcode auto-provisioning fails locally without a development cert |
| `app-debug` | × | Same auto-provisioning failure |
| `app-swiftc` | ✓ (transitively) | Foundation for `dist-swiftc` / `app-signed` |
| `app-signed` | ✓ | The ship-path target |
| `app-notarized` | optional | Use for fresh-install distribution |
| `dev` | dev only | WebDAV bridge standalone |
| `dev-nfs` | dev only | NFS bridge standalone — used heavily during pivot verification |
| `run` | × | Depends on broken `app-debug`; cull candidate |
| `run-swiftc` | dev only | Build + launch with stderr capture |
| `dist` | × | Xcode-based; same auto-provisioning failure |
| `dist-swiftc` | ✓ (transitively) | Stages the app into `dist/` for re-signing |
| `dist-dmg` | possibly | Used by CI; verify before shipping |
| `clean` | ✓ | |
| `reset-onboarding` | dev only | Clears the first-launch flag |

**Cull candidates for the next housekeeping pass:** `nfs-stub`, `run`,
`app`, `app-debug`, `dist` (all the Xcode-based targets that fail
locally), and possibly `helper` + `BUNDLE_HELPER` once the WebDAV path
is fully retired.

### Open polish item carried into v0.3.1

The Finder Locations sidebar entry shows `<DeviceName>.local` (e.g.
`XQ-BT52.local`) because the NFS mount source is the bridge's
mDNS-registered hostname. Stripping the `.local` suffix would require
either re-introducing a privileged path for `/etc/hosts` editing
(reversing the helper-free simplification) or switching from
`/sbin/mount` to `NetFSMountURLAsync` and probing whether NetFS exposes
a volume-name override for NFS URLs. Tracked but not gating ship.

## CI pipeline: where it bit us, what to do about it

The v0.3.x release sequence shipped two broken `.dmg`s in succession
on 2026-05-09 before landing a working v0.3.2. Worth recording how
each broke and how to make CI catch the next one before it ships.

### What happened

**v0.3.0:** code built, signed, and notarized cleanly. CI's only
correctness gate is `codesign --verify --strict` and Apple's notary
service — both passed. But the workflow signed the bundle with
`MenuBarApp/Comprador.entitlements`, which contains the
`com.apple.developer.system-extension.install` entitlement (added for
a planned-but-not-shipping DriverKit USB extension). That entitlement
requires `Contents/embedded.provisionprofile` in the bundle, which
the workflow didn't provision. macOS AMFI rejected at `execve(2)`
time with `-413 "No matching profile found"`.

The local-build path uses `Comprador.debug.entitlements` (no
system-extension key), so the entire 2026-05-08 verification session
ran on a different entitlements set than the released `.dmg` did. The
issue could not surface until a user ran the `.dmg`.

**v0.3.1 (the hotfix tag):** tag was pushed against the local
`origin/master` ref, which was stale at the moment of tagging — the
PR #9 merge had landed on GitHub but the local fetch hadn't. `git tag
-a v0.3.1 origin/master` resolved to the v0.3.0 commit, and CI rebuilt
the same broken code under the new tag name.

**v0.3.2:** tagged against the freshly-fetched `origin/master`. Built
clean, AMFI accepts, this is the working release.

### What CI should do differently

Three options, ordered cheap → thorough.

**A. Add `workflow_dispatch` + always-upload artifact.**
Ten lines of YAML. Lets us click a "Run workflow" button on the
Release page in the Actions UI, pick any branch, get a fully
notarized `.dmg` as a workflow artifact (download from the run's
Artifacts panel) without creating a public release. Catches the
"does the pipeline produce a sane artifact at all" question.

```yaml
on:
  push:
    tags: ['v*']
  workflow_dispatch:        # adds a "Run workflow" button

jobs:
  build-sign-notarize:
    # ... existing steps ...

    - name: Upload .dmg as workflow artifact
      uses: actions/upload-artifact@v4
      with:
        name: Comprador-dmg
        path: dist/Comprador.dmg
        retention-days: 30

    - name: Upload to GitHub Release
      if: startsWith(github.ref, 'refs/tags/v')   # tag-push only
      uses: softprops/action-gh-release@v1
      with:
        files: dist/Comprador.dmg
```

**B. Add a smoke-test launch step.** GitHub macos-14 runners are
ephemeral and headless, but the kernel still runs AMFI checks at
`execve(2)`. Tonight's `-413` rejection would have failed this:

```yaml
- name: Smoke-test launch
  run: |
    cp -R dist/Comprador.app /tmp/Comprador.app
    /tmp/Comprador.app/Contents/MacOS/Comprador & PID=$!
    sleep 3
    if ! ps -p $PID > /dev/null; then
      echo "::error::Binary was killed within 3 s of launch (likely AMFI / Gatekeeper rejection)"
      exit 1
    fi
    kill $PID
```

The Comprador menu-bar app starts cleanly without a UI surface (it's
just `NSStatusItem`), and exits clean on SIGTERM. AMFI / library
validation / hardened runtime / signing-chain mismatches all surface
as immediate process death, which is exactly what this catches.

**C. Two-workflow split.** `prerelease.yml` runs on every PR and on
`workflow_dispatch`: builds + signs + notarizes + smoke-tests +
uploads artifact. `release.yml` runs only on tag push, runs the same
pipeline, AND attaches the artifact to a GitHub release.

Strict separation: PRs are auto-verified before merge; tag pushes can
only ship code that has already passed in PR.

### Recommendation

**A + B together as part of v0.3.x housekeeping.** Twenty lines of
YAML total, gives "build a notarized `.dmg` from any branch, verify
it launches, ship only when ready." C is the rigorous version once
PR-driven release verification feels like the right discipline; not
needed for a single-developer project.

### Process lessons from v0.3.1's wrong-commit tag

CI cannot catch "the tag points at the wrong commit." That's a
process issue. Two cheap mitigations:

1. **Always `git fetch origin` before `git tag origin/master`.** A
   stale local ref is the failure mode; refreshing eliminates it.
2. **Explicit verification:** before pushing a tag, `git log -1
   <tag>` and visually confirm the commit subject matches what you
   meant to ship. Tonight's broken v0.3.1 tag pointed at "Merge pull
   request #8" instead of "Merge pull request #9" — the diff would
   have been visible.

Or: have CI tag automatically based on a labelled PR merge, removing
the manual `git tag` step. But that requires the project to
internalize a PR-driven release workflow it doesn't currently use.

### Retracting a broken release

When a release ships broken (as v0.3.0 and v0.3.1 did tonight), the
gentlest recovery preserves history while keeping users away from the
broken artifact:

```bash
gh release edit v0.3.0 --prerelease \
  --notes "Broken — install v0.3.2 instead. (Original notes below.)\n\n<original notes>"
gh release edit v0.3.1 --prerelease \
  --notes "Broken — install v0.3.2 instead. (Original notes below.)\n\n<original notes>"
```

Marking as `--prerelease` removes the "Latest Release" badge so anyone
hitting the releases page sees the working version as canonical.
Editing the notes with a one-line warning at the top means a user who
lands directly on the broken release's page sees the warning.

Don't delete the tags or releases — they're the historical record of
what happened. The lesson is more useful preserved than erased.
