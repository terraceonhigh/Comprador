import Cocoa
import ServiceManagement

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var deviceWatcher: DeviceWatcher!
    private var bridge: BridgeProcess?
    private var mountManager = MountManager()
    private var resumeCompanion: ResumeCompanion?

    // Current state
    private var connectedDevice: USBDevice?
    private var isConnecting = false  // lock out spurious events during connection
    private var pendingAttach: USBDevice?  // reattach queued while unmount was in flight (entry 19a)
    private var registeredHostname: String?  // hostname currently in /etc/hosts via helper
    private var welcomeController: WelcomeWindowController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Clear out any leftover webdav mounts from a prior session — otherwise
        // NetFS auto-suffixes today's mount as /Volumes/Pixel-6-1 and Finder
        // ends up showing duplicates.
        MountManager.cleanupStaleMounts()

        setupStatusItem()
        setupDeviceWatcher()
        updateIcon(state: .idle)
        presentWelcomeIfNeeded()
    }

    func applicationWillTerminate(_ notification: Notification) {
        Task {
            await teardown()
        }
    }

    // MARK: - Status Item

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        rebuildMenu()
    }

    private func rebuildMenu() {
        let menu = NSMenu()

        if let device = connectedDevice {
            let deviceItem = NSMenuItem(title: device.displayName, action: nil, keyEquivalent: "")
            deviceItem.isEnabled = false
            menu.addItem(deviceItem)

            if mountManager.isMounted {
                menu.addItem(NSMenuItem(title: "Show in Finder",
                                        action: #selector(showInFinder),
                                        keyEquivalent: "f"))
                menu.addItem(NSMenuItem(title: "Eject \(device.displayName)",
                                        action: #selector(ejectDevice),
                                        keyEquivalent: "e"))
#if DEBUG
                menu.addItem(NSMenuItem.separator())
                menu.addItem(NSMenuItem(title: "⚡ Synthetic Flutter",
                                        action: #selector(syntheticFlutter),
                                        keyEquivalent: ""))
#endif
            } else if isConnecting {
                let connecting = NSMenuItem(title: "Connecting…", action: nil, keyEquivalent: "")
                connecting.isEnabled = false
                menu.addItem(connecting)
            }
        } else {
            let noDevice = NSMenuItem(title: "No device connected", action: nil, keyEquivalent: "")
            noDevice.isEnabled = false
            menu.addItem(noDevice)
        }

        menu.addItem(NSMenuItem.separator())

        let loginItem = NSMenuItem(title: "Start at Login",
                                   action: #selector(toggleLoginItem),
                                   keyEquivalent: "")
        loginItem.state = LoginItem.isEnabled ? .on : .off
        menu.addItem(loginItem)

        let helperLabel: String
        switch HelperClient.statusDescription {
        case "enabled":          helperLabel = "Helper installed"
        case "requiresApproval": helperLabel = "Helper needs approval…"
        default:                 helperLabel = "Install helper…"
        }
        let helperItem = NSMenuItem(title: helperLabel,
                                    action: #selector(installHelper),
                                    keyEquivalent: "")
        helperItem.state = HelperClient.isEnabled ? .on : .off
        menu.addItem(helperItem)

        menu.addItem(NSMenuItem(title: "Quit Comprador",
                                action: #selector(quitApp),
                                keyEquivalent: "q"))

        statusItem.menu = menu
    }

    @objc private func installHelper() {
        installHelperFlow()
    }

    @objc private func toggleLoginItem() {
        if LoginItem.isEnabled {
            LoginItem.disable()
        } else if LoginItem.requiresApproval {
            // User previously disabled in System Settings — open the pane
            // so they can flip it back on.
            if let url = URL(string: "x-apple.systempreferences:com.apple.LoginItems-Settings.extension") {
                NSWorkspace.shared.open(url)
            }
        } else {
            LoginItem.enable()
        }
        rebuildMenu()
    }

    /// Register the helper, open Login Items, then poll for approval so we
    /// can confirm success without waiting for the next device attach.
    private func installHelperFlow() {
        HelperClient.register()
        if let url = URL(string: "x-apple.systempreferences:com.apple.LoginItems-Settings.extension") {
            NSWorkspace.shared.open(url)
        }
        rebuildMenu()

        // Poll for ~60s. If the user flips the toggle, congratulate;
        // otherwise leave a hint via the menu state and move on.
        Task { [weak self] in
            for _ in 0..<60 {
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                if HelperClient.isEnabled {
                    await MainActor.run { [weak self] in
                        self?.rebuildMenu()
                        NSApp.activate(ignoringOtherApps: true)
                        let done = NSAlert()
                        done.messageText = "Helper installed"
                        done.informativeText = "Future devices will mount with clean names like \"/Volumes/Pixel-6\" instead of \"/Volumes/Pixel-6.local\"."
                        done.alertStyle = .informational
                        done.addButton(withTitle: "OK")
                        done.runModal()
                    }
                    return
                }
            }
        }
    }

    /// Show the SwiftUI welcome window once on first launch. Replaces the
    /// previous pair of NSAlert prompts (login item + helper). The helper
    /// is no longer surfaced on first launch — the menu bar item still
    /// offers it for users who want clean volume names.
    private func presentWelcomeIfNeeded() {
        guard WelcomeWindowController.shouldPresent() else { return }

        // Tiny delay so the menu bar icon is in place before the window
        // appears; otherwise the user sees the window without a status item
        // to dismiss back to.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) { [weak self] in
            guard let self = self else { return }
            let controller = WelcomeWindowController()
            self.welcomeController = controller
            controller.present(onClose: { [weak self] in
                self?.welcomeController = nil
                self?.rebuildMenu()  // login item state may have changed
            })
        }
    }


    // MARK: - Icon State

    enum IconState {
        case idle
        case connecting
        case mounted
        case error
    }

    private static let pulseAnimationKey = "comprador.pulse"

    private func updateIcon(state: IconState) {
        guard let button = statusItem.button else { return }

        let symbolName: String
        switch state {
        case .idle, .connecting:
            symbolName = "externaldrive"
        case .mounted:
            symbolName = "externaldrive.fill"
        case .error:
            symbolName = "externaldrive.badge.xmark"
        }

        button.image = NSImage(systemSymbolName: symbolName, accessibilityDescription: "Comprador")
        button.image?.size = NSSize(width: 18, height: 18)

        // Throb the icon while we're trying to connect so the user has a
        // visible signal that the system is working — otherwise the
        // 5–30s wait between attach and mount looks like nothing is
        // happening. Steady icon for every other state.
        if state == .connecting {
            startPulse(on: button)
        } else {
            stopPulse(on: button)
        }
    }

    private func startPulse(on button: NSStatusBarButton) {
        button.wantsLayer = true
        guard let layer = button.layer,
              layer.animation(forKey: AppDelegate.pulseAnimationKey) == nil
        else { return }

        let pulse = CABasicAnimation(keyPath: "opacity")
        pulse.fromValue = 1.0
        pulse.toValue = 0.35
        pulse.duration = 0.9
        pulse.autoreverses = true
        pulse.repeatCount = .infinity
        pulse.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
        layer.add(pulse, forKey: AppDelegate.pulseAnimationKey)
    }

    private func stopPulse(on button: NSStatusBarButton) {
        button.layer?.removeAnimation(forKey: AppDelegate.pulseAnimationKey)
        button.layer?.opacity = 1.0
    }

    // MARK: - Device Watcher

    private func setupDeviceWatcher() {
        deviceWatcher = DeviceWatcher()
        deviceWatcher.start(
            onAttach: { [weak self] device in
                DispatchQueue.main.async {
                    self?.handleDeviceAttached(device)
                }
            },
            onDetach: { [weak self] device in
                DispatchQueue.main.async {
                    self?.handleDeviceDetached(device)
                }
            }
        )
    }

    private func handleDeviceAttached(_ device: USBDevice) {
        NSLog("Comprador: Device attached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X)",
              device.vendorID, device.productID)

        // Ignore attach events while we're already connecting or mounted
        if isConnecting {
            NSLog("Comprador: Ignoring attach — connection already in progress")
            return
        }
        if mountManager.isMounted {
            NSLog("Comprador: Reattach while unmount in flight — queuing (entry 19a)")
            pendingAttach = device
            return
        }

        connectedDevice = device
        isConnecting = true
        updateIcon(state: .connecting)
        rebuildMenu()

        Task {
            await connectDevice(device)
        }
    }

    private func handleDeviceDetached(_ device: USBDevice) {
        NSLog("Comprador: Device detached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X)",
              device.vendorID, device.productID)

        // Ignore spurious detach events during connection — USB re-enumeration
        // causes rapid detach/attach cycles when the phone switches to MTP mode
        if isConnecting {
            NSLog("Comprador: Ignoring detach — connection in progress (USB re-enumeration)")
            return
        }

        // Only tear down if we're actually mounted
        guard mountManager.isMounted || bridge?.isRunning == true else {
            connectedDevice = nil
            updateIcon(state: .idle)
            rebuildMenu()
            return
        }

        Task {
            await teardown()
            await MainActor.run {
                connectedDevice = nil
                updateIcon(state: .idle)
                rebuildMenu()
                if let queued = pendingAttach {
                    pendingAttach = nil
                    handleDeviceAttached(queued)
                }
            }
        }
    }

    @objc private func showInFinder() {
        guard let path = mountManager.mountPath else { return }
        NSWorkspace.shared.open(path)
    }

    @objc private func ejectDevice() {
        NSLog("Comprador: Eject requested")
        isConnecting = false
        pendingAttach = nil
        Task {
            await teardown()
            await MainActor.run {
                connectedDevice = nil
                updateIcon(state: .idle)
                rebuildMenu()
            }
        }
    }

    @objc private func quitApp() {
        Task {
            await teardown()
            await MainActor.run {
                NSApplication.shared.terminate(nil)
            }
        }
    }

    // MARK: - Bridge + Mount Lifecycle

    private func connectDevice(_ device: USBDevice) async {
        // Ensure any previous bridge is fully stopped
        await teardown()

        // Wait for USB to fully settle — the phone does multiple
        // detach/reattach cycles when switching to MTP mode
        try? await Task.sleep(nanoseconds: 5_000_000_000)

        // Retry with increasing delay
        let retryDelays: [UInt64] = [0, 3, 5] // seconds before each attempt

        for (attempt, delaySec) in retryDelays.enumerated() {
            guard isConnecting else { return } // cancelled

            if delaySec > 0 {
                try? await Task.sleep(nanoseconds: delaySec * 1_000_000_000)
            }

            let bp = BridgeProcess()
            self.bridge = bp

            do {
                // Start the bridge with no preferred host. The bridge does
                // its own mDNS dance and prints PORT/HOST/DEVICE on stdout.
                // Pass the device IDs so BridgeProcess can run the IOKit
                // preflight (seize + re-enumerate) and break the kernel's
                // bind on the USB interface before libusb's claim attempt.
                let port = try await bp.start(
                    seizeForVendor: device.vendorID,
                    seizeForProduct: device.productID
                )

                // Prefer the libmtp-derived friendly name (Android's
                // Settings.Global.DEVICE_NAME → MTP DeviceFriendlyName →
                // LIBMTP_Get_Friendlyname). Falls back to the IOKit USB
                // product string only if libmtp gave us nothing.
                let displayName = bp.deviceName ?? device.displayName

                // If the helper is approved, override the bridge's
                // mDNS-derived `.local` hostname with a clean single-label
                // name pulled from /etc/hosts. Falls back to bp.host on
                // any failure, which still gives the user the .local form.
                var mountHost = bp.host
                if HelperClient.isEnabled,
                   let cleanLabel = registerCleanHostname(named: displayName) {
                    mountHost = cleanLabel
                }

                let mountedURL = try await mountManager.mount(host: mountHost, port: port, displayName: displayName)

                // Start the resumable-upload companion. It polls the bridge's
                // /_comprador/sessions endpoint and, when the bridge reports
                // a chunked-PUT truncation, finds the source file via
                // NSMetadataQuery and streams the missing tail back through
                // /_comprador/sessions/<id>/append. The polling itself keeps
                // the bridge's `companionRegistered` gate open, which is
                // what allows the bridge to return 200 OK on truncation
                // (so Finder doesn't show -36) instead of falling back to
                // the honest error.
                let bridgeURL = URL(string: "http://\(bp.host):\(port)/")!
                let companion = ResumeCompanion(bridgeURL: bridgeURL)
                companion.start()
                self.resumeCompanion = companion

                await MainActor.run {
                    NSLog("Comprador: Device mounted as volume")
                    isConnecting = false
                    updateIcon(state: .mounted)
                    rebuildMenu()
                    NSWorkspace.shared.open(mountedURL)
                }
                return // success
            } catch let bridgeErr as BridgeError where bridgeErr == .timeout {
                NSLog("Comprador: Bridge timeout — prompting user")
                BridgeProcess.postFileTransferNotification()
                bp.stop()
                self.bridge = nil
                await MainActor.run {
                    isConnecting = false
                    updateIcon(state: .error)
                    rebuildMenu()
                }
                return // don't retry timeouts
            } catch let err {
                bp.stop()
                self.bridge = nil
                if attempt < retryDelays.count - 1 {
                    NSLog("Comprador: Attempt %d failed (%@), retrying...", attempt + 1, err.localizedDescription)
                } else {
                    NSLog("Comprador: All attempts failed — %@", err.localizedDescription)
                    // Same recovery hint as the timeout path — we still
                    // can't claim the USB interface, almost always because
                    // the descriptor is stale (PTP) even though the phone
                    // shows MTP, and macOS daemons hold the interface.
                    BridgeProcess.postFileTransferNotification()
                    await MainActor.run {
                        isConnecting = false
                        updateIcon(state: .error)
                        rebuildMenu()
                    }
                }
            }
        }
    }

    private func teardown() async {
        resumeCompanion?.stop()
        resumeCompanion = nil
        if mountManager.isMounted {
            await mountManager.unmount()
        }
        bridge?.stop()
        bridge = nil

        if let host = registeredHostname {
            do {
                try HelperClient.removeHost(host)
            } catch {
                NSLog("Comprador: helper removeHost(%@) failed: %@",
                      host, error.localizedDescription)
            }
            registeredHostname = nil
        }
    }

#if DEBUG
    /// Fires a synthetic detach+reattach pair synchronously on the main thread,
    /// reproducing the entry 19a race: handleDeviceDetached queues a teardown Task
    /// and returns; handleDeviceAttached runs before that Task executes, sees
    /// isMounted == true, and should queue via pendingAttach rather than discarding.
    @objc private func syntheticFlutter() {
        guard let device = connectedDevice else { return }
        NSLog("Comprador: ⚡ synthetic flutter — firing detach+reattach on \(device.displayName)")
        handleDeviceDetached(device)
        handleDeviceAttached(device)
    }
#endif

    /// Sanitises a friendly device name into a DNS label and asks the
    /// privileged helper to point it at 127.0.0.1 in /etc/hosts. Returns
    /// the hostname on success, or nil if the name didn't yield a valid
    /// label or the helper rejected it (in which case the caller should
    /// fall back to whatever the bridge advertised — typically mDNS).
    private func registerCleanHostname(named friendlyName: String) -> String? {
        let label = AppDelegate.sanitizeHostname(friendlyName)
        guard !label.isEmpty else { return nil }
        do {
            try HelperClient.addHost(label)
            registeredHostname = label
            NSLog("Comprador: registered hostname %@ via helper", label)
            return label
        } catch {
            NSLog("Comprador: helper addHost(%@) failed: %@",
                  label, error.localizedDescription)
            return nil
        }
    }

    /// Convert a friendly device name into a single-label DNS hostname
    /// matching the helper's regex `^[A-Za-z][A-Za-z0-9-]{0,62}$`.
    ///
    /// Examples:
    ///   "Pixel 6"          → "Pixel-6"
    ///   "Galaxy S24 Ultra" → "Galaxy-S24-Ultra"
    ///   "OnePlus 12"       → "OnePlus-12"
    ///   "12 Pro"           → "Phone-12-Pro"   (must start with letter)
    ///   "SM-S921B"         → "SM-S921B"       (Samsung internal code)
    ///   "Galaxy 갤럭시"    → "Galaxy"          (non-ASCII letters dropped)
    ///   ""                 → ""               (caller falls back to mDNS)
    static func sanitizeHostname(_ name: String) -> String {
        // Treat common separators as word boundaries.
        var s = name.replacingOccurrences(of: " ", with: "-")
                    .replacingOccurrences(of: "_", with: "-")
                    .replacingOccurrences(of: ".", with: "-")

        // Drop everything that isn't ASCII A-Z, a-z, 0-9, or hyphen. Swift's
        // CharacterSet.letters / .decimalDigits are Unicode-wide — they
        // accept 갤, 中, é, ٠ etc. — which the ASCII-only helper regex
        // would then reject silently.
        s = s.replacingOccurrences(of: "[^A-Za-z0-9-]",
                                   with: "",
                                   options: .regularExpression)
        // Collapse runs of hyphens to one and trim.
        s = s.replacingOccurrences(of: "-+",
                                   with: "-",
                                   options: .regularExpression)
        s = s.trimmingCharacters(in: CharacterSet(charactersIn: "-"))

        if s.isEmpty { return "" }

        // Must start with a letter; if it doesn't, prepend one. ASCII-only
        // because of the strip above.
        if let first = s.first, !first.isLetter {
            s = "Phone-" + s
        }
        if s.count > 63 {
            s = String(s.prefix(63)).trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        }
        return s
    }
}
