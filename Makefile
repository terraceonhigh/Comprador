BRIDGE_OUT   := build/bridge
HELPER_OUT   := build/comprador-helper
ICTEST1_OUT  := build/ictest1
ICTEST2_OUT  := build/ictest2
APP_NAME   := Comprador
GO         := /opt/homebrew/bin/go
DERIVED    := $(HOME)/Library/Developer/Xcode/DerivedData
LIBMTP_DYLIB := /opt/homebrew/opt/libmtp/lib/libmtp.9.dylib
LIBUSB_DYLIB := /opt/homebrew/opt/libusb/lib/libusb-1.0.0.dylib
DIST_DIR   := dist

# Build identity stamped into both the Go bridge and the Swift app, so the
# user can verify "the binary I'm running has the source I think it does."
# Format: short SHA + "-dirty" if the worktree has uncommitted changes.
BUILD_ID := $(shell git rev-parse --short HEAD 2>/dev/null)$(shell git diff --quiet 2>/dev/null || echo "-dirty")

# Release version stamped into the .app's Info.plist CFBundleShortVersionString
# so macOS reports the actual release (not the long-stale "0.1.0" hardcoded
# in MenuBarApp/Info.plist). BUILD_ID rides into CFBundleVersion so .diag
# files, the About box, and Spotlight metadata can name the exact commit.
#
# Bumped manually at release-cut time, alongside CHANGELOG.md and the tag.
# Worktree-aware: dev builds report "<version>-dev" so they're never confused
# with a tagged release.
RELEASE_VERSION := 0.4.0

.PHONY: bridge build-all bridge-test helper helper-test ictest1 ictest2 test-md5 prefetch-probe icon app app-debug app-signed app-notarized app-swiftc dev dev-nfs galatea-dev galatea-mount galatea-umount run run-swiftc dist dist-swiftc dist-dmg clean reset-onboarding

ICON_SRC := images/icon.png
ICON_OUT := MenuBarApp/Resources/Comprador.icns

# Generate the .icns from the single committed source PNG. We only commit
# one icon.png; the .iconset directory and the final .icns are build
# artifacts (gitignored). Both the swiftc and Xcode paths depend on this
# target so the .icns exists at build time.
icon: $(ICON_OUT)

$(ICON_OUT): $(ICON_SRC)
	@mkdir -p build/Comprador.iconset
	@for sz in 16 32 64 128 256 512 1024; do \
		sips -Z $$sz "$(ICON_SRC)" --out "build/Comprador.iconset/_$$sz.png" >/dev/null 2>&1; \
	done
	@cp build/Comprador.iconset/_16.png   build/Comprador.iconset/icon_16x16.png
	@cp build/Comprador.iconset/_32.png   build/Comprador.iconset/icon_16x16@2x.png
	@cp build/Comprador.iconset/_32.png   build/Comprador.iconset/icon_32x32.png
	@cp build/Comprador.iconset/_64.png   build/Comprador.iconset/icon_32x32@2x.png
	@cp build/Comprador.iconset/_128.png  build/Comprador.iconset/icon_128x128.png
	@cp build/Comprador.iconset/_256.png  build/Comprador.iconset/icon_128x128@2x.png
	@cp build/Comprador.iconset/_256.png  build/Comprador.iconset/icon_256x256.png
	@cp build/Comprador.iconset/_512.png  build/Comprador.iconset/icon_256x256@2x.png
	@cp build/Comprador.iconset/_512.png  build/Comprador.iconset/icon_512x512.png
	@cp build/Comprador.iconset/_1024.png build/Comprador.iconset/icon_512x512@2x.png
	@rm build/Comprador.iconset/_*.png
	iconutil -c icns build/Comprador.iconset -o $(ICON_OUT)
	@echo "Generated $(ICON_OUT) from $(ICON_SRC)"

bridge:
	cd bridge && CGO_CFLAGS="-I$(CURDIR)/bridge/cvendor" CGO_LDFLAGS="-L/opt/homebrew/lib" $(GO) build -ldflags="-X main.BuildID=$(BUILD_ID)" -o ../$(BRIDGE_OUT) .

# Tree-wide compile + vendor-consistency check (every package and cmd tool).
# Catches "inconsistent vendoring" after vendor edits — broader than `bridge`,
# which only builds the main artifact.
build-all:
	cd bridge && CGO_CFLAGS="-I$(CURDIR)/bridge/cvendor" CGO_LDFLAGS="-L/opt/homebrew/lib" $(GO) build ./...

helper:
	cd helper && $(GO) build -o ../$(HELPER_OUT) .

helper-test:
	cd helper && $(GO) test -v ./...

# Bridge mtp-package tests. cgo flags must be set explicitly because go test
# doesn't inherit them from the Makefile's `bridge` build rule.
bridge-test:
	cd bridge && CGO_CFLAGS="-I$(CURDIR)/bridge/cvendor" CGO_LDFLAGS="-L/opt/homebrew/lib" $(GO) test -v ./...

# Research probe: Test 1 from docs/RESEARCH-IMAGECAPTURECORE.md.
# Tests whether ICDevice.requestOpenSession coexists with ptpcamerad.
# Output goes into RESEARCH-IMAGECAPTURECORE.md §Test 1 Results.
ictest1:
	@mkdir -p build
	swiftc -framework ImageCaptureCore -framework Foundation \
		bridge/cmd/ictest1/main.swift -o $(ICTEST1_OUT)
	@echo "Built: $(ICTEST1_OUT)"
	@echo "Run:   ./$(ICTEST1_OUT)"

# Phone-side md5 verification of a directory transfer. Developer-only
# (uses ADB; never bundled into the user-facing app). Compares Mac
# source against on-phone md5sums computed by adb shell, bypassing the
# bridge entirely — so a bridge-side bug can't mask itself by being
# self-consistent. Pass MAC_DIR and PHONE_DIR as args.
#
#   make test-md5 MAC_DIR=~/Documents/ECON101 PHONE_DIR=/storage/emulated/0/Download/ECON101
test-md5:
	@if [ -z "$(MAC_DIR)" ] || [ -z "$(PHONE_DIR)" ]; then \
	  echo "Usage: make test-md5 MAC_DIR=<path> PHONE_DIR=<path>"; \
	  echo "  example:"; \
	  echo "    make test-md5 MAC_DIR=~/Documents/ECON101 PHONE_DIR=/storage/emulated/0/Download/ECON101"; \
	  exit 64; \
	fi
	@COMPRADOR_TESTING_ADB=1 ./test-md5.sh "$(MAC_DIR)" "$(PHONE_DIR)"

# Research probe: Test 2 from docs/RESEARCH-IMAGECAPTURECORE.md.
# Measures sequential read throughput via requestReadDataFromFile.
ictest2:
	@mkdir -p build
	swiftc -framework ImageCaptureCore -framework Foundation -framework CryptoKit \
		bridge/cmd/ictest2/main.swift -o $(ICTEST2_OUT)
	@echo "Built: $(ICTEST2_OUT)"
	@echo "Run:   ./$(ICTEST2_OUT)"

# Empirical probe for the prefetch redesign (docs/PLAN-PREFETCH-REDESIGN.md
# Step 1). Measures whether LIBMTP_GetPartialObject is viable for the
# chunked-yield design. Run with a phone connected in File Transfer mode;
# auto-picks the first file > 100 MB on the device.
#
#   make prefetch-probe
#   ./build/prefetch-probe                      # 4 MB chunks (default), 64 MB total
#   ./build/prefetch-probe -chunk=16 -bytes=128 # 16 MB chunks, 128 MB total
#   ./build/prefetch-probe -skip-control        # skip the full-object read
prefetch-probe:
	@mkdir -p build
	cd bridge && $(GO) build -o ../build/prefetch-probe ./cmd/prefetch-probe
	@echo "Built: build/prefetch-probe"
	@echo "Run:   ./build/prefetch-probe (with a phone connected in File Transfer mode)"

# Bundle bridge + all dylibs into an app directory, fix rpaths
define BUNDLE_BRIDGE
	mkdir -p "$(1)/Contents/Frameworks" "$(1)/Contents/Resources"; \
	rm -f "$(1)/Contents/Resources/bridge" \
	      "$(1)/Contents/Frameworks/libmtp.9.dylib" \
	      "$(1)/Contents/Frameworks/libusb-1.0.0.dylib"; \
	cp $(BRIDGE_OUT) "$(1)/Contents/Resources/bridge"; \
	cp $(LIBMTP_DYLIB) "$(1)/Contents/Frameworks/libmtp.9.dylib"; \
	cp $(LIBUSB_DYLIB) "$(1)/Contents/Frameworks/libusb-1.0.0.dylib"; \
	install_name_tool -change $(LIBMTP_DYLIB) \
		@executable_path/../Frameworks/libmtp.9.dylib \
		"$(1)/Contents/Resources/bridge"; \
	install_name_tool -change $(LIBUSB_DYLIB) \
		@executable_path/../Frameworks/libusb-1.0.0.dylib \
		"$(1)/Contents/Resources/bridge"; \
	install_name_tool -change $(LIBUSB_DYLIB) \
		@executable_path/../Frameworks/libusb-1.0.0.dylib \
		"$(1)/Contents/Frameworks/libmtp.9.dylib"; \
	codesign --force --sign - "$(1)/Contents/Frameworks/libmtp.9.dylib"; \
	codesign --force --sign - "$(1)/Contents/Frameworks/libusb-1.0.0.dylib"; \
	codesign --force --sign - "$(1)/Contents/Resources/bridge"; \
	echo "Bundled bridge + libmtp + libusb into $(1)"
endef

# Bundle the privileged helper binary + LaunchDaemon plist. SMAppService.daemon
# expects the plist at Contents/Library/LaunchDaemons/<plist> and the binary
# referenced by the plist's BundleProgram (relative to the app bundle root).
define BUNDLE_HELPER
	mkdir -p "$(1)/Contents/Library/LaunchDaemons"; \
	rm -f "$(1)/Contents/MacOS/comprador-helper" \
	      "$(1)/Contents/Library/LaunchDaemons/com.comprador.helper.plist"; \
	cp $(HELPER_OUT) "$(1)/Contents/MacOS/comprador-helper"; \
	cp helper/com.comprador.helper.plist "$(1)/Contents/Library/LaunchDaemons/"; \
	codesign --force --sign - "$(1)/Contents/MacOS/comprador-helper"; \
	echo "Bundled helper into $(1)"
endef

app: bridge icon
	xcodebuild -project MenuBarApp/$(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration Release \
	           build
	@APP_DIR=$$(find $(DERIVED) -path "*/Release/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	if [ -n "$$APP_DIR" ]; then \
		$(call BUNDLE_BRIDGE,$$APP_DIR); \
	fi

app-debug: bridge icon
	xcodebuild -project MenuBarApp/$(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration Debug \
	           build
	@APP_DIR=$$(find $(DERIVED) -path "*/Debug/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	if [ -n "$$APP_DIR" ]; then \
		$(call BUNDLE_BRIDGE,$$APP_DIR); \
	fi

dev: bridge
	DYLD_LIBRARY_PATH=/opt/homebrew/lib ./$(BRIDGE_OUT) 2>&1

# Run the NFS bridge directly against a live MTP device.
# The bridge will print PORT=N and the exact sudo mount command to use.
# Use this to verify Phase 2/3 NFS behaviour without needing the helper.
dev-nfs: bridge
	DYLD_LIBRARY_PATH=/opt/homebrew/lib ./$(BRIDGE_OUT) --nfs 2>&1

# Phase-4 verification harness (mercer/galatea-integration): serve the live MTP
# device over Galatea's userspace NFSv4 server instead of the patched
# willscott/go-nfs. Read-only for now (mtpfsal mutations return ROFS). Built in
# the standalone bridge-only harness, now that galatea is a normal vendored dep
# (v0.2.0-alpha, manually vendored — `go mod vendor` is never run because it
# would clobber the patched go-nfs fork). The production `bridge --nfs` path now
# serves Galatea too; this harness is kept for serving without the menu-bar app.
# Prints the vers=4.0 mount_nfs command. See bridge/cmd/galatea-serve, TODO.md.
GALATEA_OUT := build/galatea-serve
galatea-dev:
	@mkdir -p build
	cd bridge && CGO_CFLAGS="-I$(CURDIR)/bridge/cvendor" CGO_LDFLAGS="-L/opt/homebrew/lib" $(GO) build -o ../$(GALATEA_OUT) ./cmd/galatea-serve
	DYLD_LIBRARY_PATH=/opt/homebrew/lib ./$(GALATEA_OUT) 2>&1

# Mount the running galatea-dev server (pass PORT=N from its PORT= line).
# vers=4.0 (Galatea is NFSv4), unprivileged loopback mount — no root.
galatea-mount:
	@mkdir -p /tmp/galmnt
	mount_nfs -o vers=4.0,port=$(PORT),mountport=$(PORT),tcp localhost:/ /tmp/galmnt
	@mount | grep /tmp/galmnt || true

galatea-umount:
	-umount -f /tmp/galmnt 2>/dev/null || true
	@mount | grep /tmp/galmnt >/dev/null && echo "STILL MOUNTED" || echo "unmounted"

run: app-debug
	@killall $(APP_NAME) 2>/dev/null || true
	@sleep 1
	@APP_DIR=$$(find $(DERIVED) -path "*/Debug/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	echo "Launching $$APP_DIR"; \
	"$$APP_DIR/Contents/MacOS/$(APP_NAME)"

# Build a distributable .app + zip
dist: bridge icon
	xcodebuild -project MenuBarApp/$(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration Release \
	           build
	@APP_DIR=$$(find $(DERIVED) -path "*/Release/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	if [ -z "$$APP_DIR" ]; then echo "ERROR: app not found"; exit 1; fi; \
	$(call BUNDLE_BRIDGE,$$APP_DIR); \
	rm -rf $(DIST_DIR); \
	mkdir -p $(DIST_DIR); \
	cp -R "$$APP_DIR" $(DIST_DIR)/$(APP_NAME).app; \
	cd $(DIST_DIR) && zip -r $(APP_NAME).zip $(APP_NAME).app; \
	echo ""; \
	echo "=== Distribution ready ==="; \
	echo "  $(DIST_DIR)/$(APP_NAME).app"; \
	echo "  $(DIST_DIR)/$(APP_NAME).zip ($$(du -h $(DIST_DIR)/$(APP_NAME).zip | cut -f1))"; \
	echo ""; \
	echo "Testers: right-click → Open on first launch (unsigned)"

# swiftc-based build — works without full Xcode (only Command Line Tools).
# Produces build/swift/Comprador.app, ad-hoc signed.
SWIFT_APP    := build/swift/$(APP_NAME).app
SWIFT_BIN    := build/swift/$(APP_NAME)
SWIFT_SRC    := $(wildcard MenuBarApp/Sources/*.swift)
SWIFT_TARGET := arm64-apple-macosx13.0
BUILD_INFO_SWIFT := build/BuildInfo.swift

# Swift conditional-compilation flag. SWIFT_DEBUG=1 enables `#if DEBUG`
# code paths (the build-identifier copy-on-click menu item, the
# synthetic-flutter testing menu, future dev-only affordances). Default
# is production: no DEBUG, no dev menu items.
#
# Set on the command line: `make app-swiftc SWIFT_DEBUG=1` for the
# developer experience.
SWIFT_DEBUG ?=
SWIFT_DEBUG_FLAG := $(if $(SWIFT_DEBUG),-D DEBUG,)

app-swiftc: bridge helper icon
	@mkdir -p build/swift build
	@printf 'enum BuildInfo { static let id = "%s" }\n' "$(BUILD_ID)" > $(BUILD_INFO_SWIFT)
	swiftc -target $(SWIFT_TARGET) -O $(SWIFT_DEBUG_FLAG) \
		-framework Cocoa -framework SwiftUI -framework IOKit \
		-framework DiskArbitration -framework ServiceManagement \
		-framework UserNotifications \
		-o $(SWIFT_BIN) $(SWIFT_SRC) $(BUILD_INFO_SWIFT)
	@rm -rf $(SWIFT_APP)
	@mkdir -p $(SWIFT_APP)/Contents/MacOS \
	          $(SWIFT_APP)/Contents/Resources \
	          $(SWIFT_APP)/Contents/Frameworks \
	          $(SWIFT_APP)/Contents/Library/LaunchDaemons
	cp $(SWIFT_BIN) $(SWIFT_APP)/Contents/MacOS/$(APP_NAME)
	cp MenuBarApp/Info.plist $(SWIFT_APP)/Contents/Info.plist
	@# Stamp real version + git hash into the bundle's Info.plist.
	@# Must run BEFORE codesign so the signature covers the updated plist.
	@# See docs/PLAN-BUILD-IDENTITY.md for the rationale (the 2026-05-18
	@# Comprador.diag reported Version: 0.1.0 (1), masking the actual build).
	/usr/libexec/PlistBuddy \
		-c "Set :CFBundleShortVersionString $(RELEASE_VERSION)" \
		-c "Set :CFBundleVersion $(BUILD_ID)" \
		$(SWIFT_APP)/Contents/Info.plist
	cp MenuBarApp/Resources/VendorIDs.plist $(SWIFT_APP)/Contents/Resources/
	cp MenuBarApp/Resources/Comprador.icns $(SWIFT_APP)/Contents/Resources/
	@printf 'APPL????' > $(SWIFT_APP)/Contents/PkgInfo
	$(call BUNDLE_BRIDGE,$(SWIFT_APP))
	$(call BUNDLE_HELPER,$(SWIFT_APP))
	codesign --force --deep --sign - \
		--entitlements MenuBarApp/Comprador.debug.entitlements \
		--options runtime \
		$(SWIFT_APP)
	@echo ""
	@echo "Built: $(SWIFT_APP)"

SWIFT_LOG := build/comprador.log

run-swiftc: app-swiftc
	@killall $(APP_NAME) 2>/dev/null || true
	@sleep 1
	@mkdir -p build
	@echo "Launching $(SWIFT_APP)"
	@echo "Tee'ing app + bridge output to $(SWIFT_LOG)"
	@echo "  (tail in another terminal:  tail -f $(SWIFT_LOG))"
	$(SWIFT_APP)/Contents/MacOS/$(APP_NAME) 2>&1 | tee $(SWIFT_LOG)

dist-swiftc: app-swiftc
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	cp -R $(SWIFT_APP) $(DIST_DIR)/$(APP_NAME).app
	cd $(DIST_DIR) && zip -qr $(APP_NAME).zip $(APP_NAME).app
	@echo ""
	@echo "=== Distribution ready ==="
	@echo "  $(DIST_DIR)/$(APP_NAME).app"
	@echo "  $(DIST_DIR)/$(APP_NAME).zip ($$(du -h $(DIST_DIR)/$(APP_NAME).zip | cut -f1))"
	@echo ""
	@echo "Testers: right-click → Open on first launch (ad-hoc signed)"

# Re-sign dist/Comprador.app with the local Developer ID Application
# certificate. The bundle that comes out is suitable for replacing an
# installed /Applications/Comprador.app while keeping the SMAppService
# helper registration intact (SMAppService accepts cdhash changes when
# the signature chain stays valid).
#
# Skips notarization. macOS allows opening a properly-signed but
# un-notarized app by right-click → Open on first launch. SMAppService
# itself only requires Developer ID + hardened runtime, not notarization.
#
# Uses Comprador.debug.entitlements (no com.apple.developer.system-extension.install)
# because that entitlement requires a provisioning profile, which we
# can't generate locally without an active development scheme. The
# DriverKit install path is unavailable in app-signed builds; everything
# else (USB matching, helper, NFS bridge) works.
app-signed: dist-swiftc
	@IDENTITY=$$(security find-identity -v -p codesigning \
	            | awk '/Developer ID Application/{print $$2; exit}'); \
	if [ -z "$$IDENTITY" ]; then \
	  echo "ERROR: No Developer ID Application certificate in keychain"; \
	  exit 1; \
	fi; \
	echo "Signing with $$IDENTITY"; \
	BUNDLE=$(DIST_DIR)/$(APP_NAME).app; \
	for path in \
	  "$$BUNDLE/Contents/Frameworks/libmtp.9.dylib" \
	  "$$BUNDLE/Contents/Frameworks/libusb-1.0.0.dylib" \
	  "$$BUNDLE/Contents/Resources/bridge" \
	  "$$BUNDLE/Contents/MacOS/comprador-helper"; \
	do \
	  if [ -e "$$path" ]; then \
	    echo "Signing $$path"; \
	    codesign --force --options runtime --timestamp \
	      --sign "$$IDENTITY" "$$path"; \
	  fi; \
	done; \
	echo "Signing bundle"; \
	codesign --force --options runtime --timestamp \
	  --entitlements MenuBarApp/Comprador.debug.entitlements \
	  --sign "$$IDENTITY" "$$BUNDLE"; \
	codesign --verify --strict --verbose=2 "$$BUNDLE"
	@echo ""
	@echo "=== Developer-ID-signed app ready ==="
	@echo "  $(DIST_DIR)/$(APP_NAME).app"
	@echo ""
	@echo "Install with:"
	@echo "  killall Comprador 2>/dev/null; sudo rm -rf /Applications/$(APP_NAME).app && sudo cp -R $(DIST_DIR)/$(APP_NAME).app /Applications/"

# Local notarization. Submits the Developer-ID-signed app to Apple's
# notary service, waits for the verdict, then staples the ticket so
# the .app passes Gatekeeper offline.
#
# One-time setup before first use:
#   xcrun notarytool store-credentials notarytool-password \
#     --apple-id terrace@terrace.zone \
#     --team-id 5875SC35WL
# (it'll prompt for an app-specific password from appleid.apple.com)
#
# Total wall time: ~5 min (depends on Apple notary queue).
app-notarized: app-signed
	@echo "Zipping for notarytool submission..."
	@rm -f $(DIST_DIR)/$(APP_NAME).zip
	@cd $(DIST_DIR) && /usr/bin/ditto -c -k --keepParent $(APP_NAME).app $(APP_NAME).zip
	@echo "Submitting to Apple notary (this may take a few minutes)..."
	@xcrun notarytool submit $(DIST_DIR)/$(APP_NAME).zip \
	  --keychain-profile notarytool-password \
	  --wait
	@echo "Stapling ticket to .app..."
	@xcrun stapler staple $(DIST_DIR)/$(APP_NAME).app
	@xcrun stapler validate $(DIST_DIR)/$(APP_NAME).app
	@echo ""
	@echo "=== Notarized app ready ==="
	@echo "  $(DIST_DIR)/$(APP_NAME).app"
	@echo ""
	@echo "Install with:"
	@echo "  killall Comprador 2>/dev/null; rm -rf /Applications/$(APP_NAME).app && cp -R $(DIST_DIR)/$(APP_NAME).app /Applications/"

# Build a drag-to-Applications .dmg from the existing dist-swiftc output.
# Uses macOS's built-in hdiutil — no extra tooling. The DMG is a 100MB
# read-write scratch image that gets the .app and an /Applications
# alias dropped in, then converted to a compressed read-only image.
# That's the standard pattern for app .dmgs.
dist-dmg: dist-swiftc
	@rm -f $(DIST_DIR)/$(APP_NAME).dmg $(DIST_DIR)/$(APP_NAME)-rw.dmg
	@echo "Creating scratch DMG..."
	@hdiutil create -size 100m -fs HFS+ -volname "$(APP_NAME)" \
		-srcfolder $(DIST_DIR)/$(APP_NAME).app \
		-format UDRW $(DIST_DIR)/$(APP_NAME)-rw.dmg >/dev/null
	@echo "Mounting scratch DMG..."
	@hdiutil attach $(DIST_DIR)/$(APP_NAME)-rw.dmg \
		-mountpoint /Volumes/$(APP_NAME) -nobrowse -quiet
	@ln -sf /Applications /Volumes/$(APP_NAME)/Applications
	@hdiutil detach /Volumes/$(APP_NAME) -quiet
	@echo "Compressing..."
	@hdiutil convert $(DIST_DIR)/$(APP_NAME)-rw.dmg \
		-format UDZO -imagekey zlib-level=9 \
		-o $(DIST_DIR)/$(APP_NAME).dmg >/dev/null
	@rm -f $(DIST_DIR)/$(APP_NAME)-rw.dmg
	@echo ""
	@echo "=== DMG ready ==="
	@echo "  $(DIST_DIR)/$(APP_NAME).dmg ($$(du -h $(DIST_DIR)/$(APP_NAME).dmg | cut -f1))"
	@echo ""
	@echo "Open with: open $(DIST_DIR)/$(APP_NAME).dmg"

clean:
	rm -rf build/ dist/

# Wipe first-launch state so the welcome window appears again next run.
# Useful when iterating on onboarding UX. Kills the running app first
# because cfprefsd caches per-app defaults for live processes.
reset-onboarding:
	@killall $(APP_NAME) 2>/dev/null || true
	@defaults delete com.comprador.app Comprador.didShowWelcome 2>/dev/null || true
	@echo "Onboarding state cleared. Next run will show the welcome window."
