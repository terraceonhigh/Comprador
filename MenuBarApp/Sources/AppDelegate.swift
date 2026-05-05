import Cocoa
import ServiceManagement

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var deviceWatcher: DeviceWatcher!
    private var bridge: BridgeProcess?
    private var mountManager = MountManager()

    // Current state
    private var connectedDevice: USBDevice?
    private var isConnecting = false  // lock out spurious events during connection
    private var registeredHostname: String?  // hostname currently in /etc/hosts via helper

    func applicationDidFinishLaunching(_ notification: Notification) {
        setupStatusItem()
        setupDeviceWatcher()
        updateIcon(state: .idle)
        offerLoginItemOnFirstLaunch()
        offerHelperOnFirstLaunch()
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
                menu.addItem(NSMenuItem(title: "Eject \(device.displayName)",
                                        action: #selector(ejectDevice),
                                        keyEquivalent: "e"))
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

        menu.addItem(NSMenuItem(title: "Quit AndroidFS",
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

    /// Prompt the user to install the helper. Fires on every launch until
    /// either the helper is enabled OR the user explicitly skips with
    /// "Don't ask again". This is intentionally persistent — the cleaner
    /// volume names are the single biggest UX win and the prompt is
    /// trivially dismissable.
    ///
    /// Background-only apps (LSUIElement = true) can have NSAlert windows
    /// hidden behind whatever's in front, so we activate the app first.
    private func offerHelperOnFirstLaunch() {
        let declinedKey = "AndroidFS.declinedHelper"
        if HelperClient.isEnabled { return }
        if UserDefaults.standard.bool(forKey: declinedKey) { return }

        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
            guard let self = self else { return }
            // Bring the app forward so the alert isn't buried.
            NSApp.activate(ignoringOtherApps: true)

            let alert = NSAlert()
            alert.messageText = "Show your phone's name in Finder"
            alert.informativeText = "Without this, mounted volumes show as \"Pixel-6.local\" in Finder instead of \"Pixel-6\". The helper edits a managed block in /etc/hosts so the bridge can use a clean hostname, and only accepts single-label device names — it can't impersonate real domains.\n\nClicking Install opens System Settings → Login Items. Toggle \"AndroidFS Helper\" on to finish."
            alert.addButton(withTitle: "Install Helper")
            alert.addButton(withTitle: "Not Now")
            alert.addButton(withTitle: "Don't Ask Again")
            alert.alertStyle = .informational

            switch alert.runModal() {
            case .alertFirstButtonReturn:
                self.installHelperFlow()
            case .alertThirdButtonReturn:
                UserDefaults.standard.set(true, forKey: declinedKey)
            default:
                break // "Not Now" — ask again next launch
            }
        }
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
                    await MainActor.run {
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

    private func offerLoginItemOnFirstLaunch() {
        let key = "AndroidFS.didOfferLoginItem"
        guard !UserDefaults.standard.bool(forKey: key) else { return }
        UserDefaults.standard.set(true, forKey: key)

        // Don't prompt if the user already has it enabled or explicitly disabled it
        let status = SMAppService.mainApp.status
        guard status == .notRegistered else { return }

        DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
            let alert = NSAlert()
            alert.messageText = "Start AndroidFS at login?"
            alert.informativeText = "AndroidFS can launch automatically so your phone shows up in Finder as soon as you plug it in."
            alert.addButton(withTitle: "Start at Login")
            alert.addButton(withTitle: "Not Now")
            alert.alertStyle = .informational
            if alert.runModal() == .alertFirstButtonReturn {
                LoginItem.enable()
                self.rebuildMenu()
            }
        }
    }

    // MARK: - Icon State

    enum IconState {
        case idle
        case connecting
        case mounted
        case error
    }

    private func updateIcon(state: IconState) {
        guard let button = statusItem.button else { return }

        let symbolName: String
        switch state {
        case .idle:
            symbolName = "externaldrive"
        case .connecting:
            symbolName = "externaldrive"
        case .mounted:
            symbolName = "externaldrive.fill"
        case .error:
            symbolName = "externaldrive.badge.xmark"
        }

        button.image = NSImage(systemSymbolName: symbolName, accessibilityDescription: "AndroidFS")
        button.image?.size = NSSize(width: 18, height: 18)
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
        NSLog("AndroidFS: Device attached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X)",
              device.vendorID, device.productID)

        // Ignore attach events while we're already connecting or mounted
        if isConnecting {
            NSLog("AndroidFS: Ignoring attach — connection already in progress")
            return
        }
        if mountManager.isMounted {
            NSLog("AndroidFS: Ignoring attach — already mounted")
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
        NSLog("AndroidFS: Device detached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X)",
              device.vendorID, device.productID)

        // Ignore spurious detach events during connection — USB re-enumeration
        // causes rapid detach/attach cycles when the phone switches to MTP mode
        if isConnecting {
            NSLog("AndroidFS: Ignoring detach — connection in progress (USB re-enumeration)")
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
            }
        }
    }

    @objc private func ejectDevice() {
        NSLog("AndroidFS: Eject requested")
        isConnecting = false
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
                let preferredHost = HelperClient.isEnabled
                    ? registerCleanHostname(for: device)
                    : nil

                let port = try await bp.start(preferredHost: preferredHost)
                let displayName = bp.deviceName ?? device.displayName

                let _ = try await mountManager.mount(host: bp.host, port: port, displayName: displayName)

                await MainActor.run {
                    NSLog("AndroidFS: Device mounted as volume")
                    isConnecting = false
                    updateIcon(state: .mounted)
                    rebuildMenu()
                }
                return // success
            } catch let bridgeErr as BridgeError where bridgeErr == .timeout {
                NSLog("AndroidFS: Bridge timeout — prompting user")
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
                    NSLog("AndroidFS: Attempt %d failed (%@), retrying...", attempt + 1, err.localizedDescription)
                } else {
                    NSLog("AndroidFS: All attempts failed — %@", err.localizedDescription)
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
        if mountManager.isMounted {
            await mountManager.unmount()
        }
        bridge?.stop()
        bridge = nil

        if let host = registeredHostname {
            do {
                try HelperClient.removeHost(host)
            } catch {
                NSLog("AndroidFS: helper removeHost(%@) failed: %@",
                      host, error.localizedDescription)
            }
            registeredHostname = nil
        }
    }

    /// Sanitises the device's USB display name into a DNS label and asks
    /// the privileged helper to point it at 127.0.0.1 in /etc/hosts.
    /// Returns the hostname on success, or nil if the helper rejected it
    /// (in which case the bridge falls back to mDNS).
    private func registerCleanHostname(for device: USBDevice) -> String? {
        let label = sanitizeHostname(device.displayName)
        guard !label.isEmpty else { return nil }
        do {
            try HelperClient.addHost(label)
            registeredHostname = label
            NSLog("AndroidFS: registered hostname %@ via helper", label)
            return label
        } catch {
            NSLog("AndroidFS: helper addHost(%@) failed: %@",
                  label, error.localizedDescription)
            return nil
        }
    }

    /// Convert a friendly device name into a single-label DNS hostname
    /// matching the helper's regex `^[A-Za-z][A-Za-z0-9-]{0,62}$`.
    private func sanitizeHostname(_ name: String) -> String {
        var s = name.replacingOccurrences(of: " ", with: "-")
                    .replacingOccurrences(of: "_", with: "-")
                    .replacingOccurrences(of: ".", with: "-")
        s = s.unicodeScalars.filter {
            CharacterSet.letters.contains($0) ||
            CharacterSet.decimalDigits.contains($0) ||
            $0 == "-"
        }.map { Character($0) }.reduce("") { $0 + String($1) }
        s = s.trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        // Must start with a letter; if it doesn't, prepend one.
        if let first = s.first, !first.isLetter {
            s = "Phone-" + s
        }
        if s.count > 63 {
            s = String(s.prefix(63)).trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        }
        return s
    }
}
