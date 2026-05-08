BRIDGE_OUT := build/bridge
HELPER_OUT := build/comprador-helper
APP_NAME   := Comprador
GO         := /opt/homebrew/bin/go
DERIVED    := $(HOME)/Library/Developer/Xcode/DerivedData
LIBMTP_DYLIB := /opt/homebrew/opt/libmtp/lib/libmtp.9.dylib
LIBUSB_DYLIB := /opt/homebrew/opt/libusb/lib/libusb-1.0.0.dylib
DIST_DIR   := dist

.PHONY: bridge helper helper-test app app-debug app-swiftc dev run run-swiftc dist dist-swiftc dist-dmg clean reset-onboarding

bridge:
	cd bridge && CGO_CFLAGS="-I$(CURDIR)/bridge/cvendor" CGO_LDFLAGS="-L/opt/homebrew/lib" $(GO) build -o ../$(BRIDGE_OUT) .

helper:
	cd helper && $(GO) build -o ../$(HELPER_OUT) .

helper-test:
	cd helper && $(GO) test -v ./...

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

app: bridge
	xcodebuild -project MenuBarApp/$(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration Release \
	           build
	@APP_DIR=$$(find $(DERIVED) -path "*/Release/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	if [ -n "$$APP_DIR" ]; then \
		$(call BUNDLE_BRIDGE,$$APP_DIR); \
	fi

app-debug: bridge
	xcodebuild -project MenuBarApp/$(APP_NAME).xcodeproj \
	           -scheme $(APP_NAME) \
	           -configuration Debug \
	           build
	@APP_DIR=$$(find $(DERIVED) -path "*/Debug/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	if [ -n "$$APP_DIR" ]; then \
		$(call BUNDLE_BRIDGE,$$APP_DIR); \
	fi

dev: bridge
	./$(BRIDGE_OUT) 2>&1

run: app-debug
	@killall $(APP_NAME) 2>/dev/null || true
	@sleep 1
	@APP_DIR=$$(find $(DERIVED) -path "*/Debug/$(APP_NAME).app" -maxdepth 5 2>/dev/null | head -1); \
	echo "Launching $$APP_DIR"; \
	"$$APP_DIR/Contents/MacOS/$(APP_NAME)"

# Build a distributable .app + zip
dist: bridge
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

app-swiftc: bridge helper
	@mkdir -p build/swift
	swiftc -target $(SWIFT_TARGET) -O -D DEBUG \
		-framework Cocoa -framework SwiftUI -framework IOKit \
		-framework DiskArbitration -framework ServiceManagement \
		-framework UserNotifications \
		-o $(SWIFT_BIN) $(SWIFT_SRC)
	@rm -rf $(SWIFT_APP)
	@mkdir -p $(SWIFT_APP)/Contents/MacOS \
	          $(SWIFT_APP)/Contents/Resources \
	          $(SWIFT_APP)/Contents/Frameworks \
	          $(SWIFT_APP)/Contents/Library/LaunchDaemons
	cp $(SWIFT_BIN) $(SWIFT_APP)/Contents/MacOS/$(APP_NAME)
	cp MenuBarApp/Info.plist $(SWIFT_APP)/Contents/Info.plist
	cp MenuBarApp/Resources/VendorIDs.plist $(SWIFT_APP)/Contents/Resources/
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
