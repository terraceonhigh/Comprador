# Building AndroidFS

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

## Build Targets

```bash
# Just the Go bridge binary
make bridge

# Just the privileged helper binary
make helper

# Run the helper unit tests
make helper-test

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
├── AndroidFS.app/
│   ├── Contents/
│   │   ├── MacOS/
│   │   │   ├── AndroidFS                              # Swift menu bar app
│   │   │   └── androidfs-helper                       # Privileged helper
│   │   ├── Library/
│   │   │   └── LaunchDaemons/
│   │   │       └── com.androidfs.helper.plist         # SMAppService.daemon
│   │   ├── Resources/
│   │   │   ├── bridge                                 # Go WebDAV bridge
│   │   │   └── VendorIDs.plist                        # Android vendor IDs
│   │   ├── Frameworks/
│   │   │   ├── libmtp.9.dylib                         # MTP library
│   │   │   └── libusb-1.0.0.dylib                     # USB library
│   │   └── Info.plist
│   └── ...
└── AndroidFS.zip                                       # ~5MB, ready to share
```

All dynamic library paths are rewritten with `install_name_tool` to use
`@executable_path/../Frameworks/`, so the app is self-contained. No
Homebrew installation needed on the target Mac.

The helper plist's `BundleProgram` points at
`Contents/MacOS/androidfs-helper`; `SMAppService.daemon(plistName:)`
reads it from the bundle on first registration. The daemon stays
disabled until the user approves it under System Settings → Login Items.

## Development Workflow

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
