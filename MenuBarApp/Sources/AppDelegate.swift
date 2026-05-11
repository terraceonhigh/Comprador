import Cocoa
import ServiceManagement

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var deviceWatcher: DeviceWatcher!
    private var welcomeController: WelcomeWindowController?

    /// The currently connected device's session, or nil if no device is
    /// attached. PLAN-MULTI-DEVICE.md step 2: single optional now; step 3
    /// will widen this to `[DeviceID: DeviceSession]`.
    private var session: DeviceSession?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSLog("Comprador build: %@", BuildInfo.id)

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
            await teardownCurrentSession()
        }
    }

    // MARK: - Status Item

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        rebuildMenu()
    }

    private func rebuildMenu() {
        let menu = NSMenu()

        if let session = session {
            let deviceItem = NSMenuItem(title: session.displayName, action: nil, keyEquivalent: "")
            deviceItem.isEnabled = false
            menu.addItem(deviceItem)

            if session.isMounted {
                menu.addItem(NSMenuItem(title: "Show in Finder",
                                        action: #selector(showInFinder),
                                        keyEquivalent: "f"))
                menu.addItem(NSMenuItem(title: "Eject \(session.displayName)",
                                        action: #selector(ejectDevice),
                                        keyEquivalent: "e"))
#if DEBUG
                menu.addItem(NSMenuItem.separator())
                menu.addItem(NSMenuItem(title: "⚡ Synthetic Flutter",
                                        action: #selector(syntheticFlutter),
                                        keyEquivalent: ""))
#endif
            } else if session.isConnecting {
                let statusLine = NSMenuItem(title: session.connectingStatusTitle(),
                                            action: nil, keyEquivalent: "")
                statusLine.isEnabled = false
                menu.addItem(statusLine)
                session.connectingStatusItem = statusLine

                // Hint only shown on the WebDAV path where the ~90s
                // NetFSMountURLSync wait dominates the cycle. NFS connects
                // in a few seconds, so the hint would be misleading there.
                if session.bridgeProto != "nfs" {
                    let hint = NSMenuItem(title: "Finder takes about 90 seconds to attach the volume",
                                          action: nil, keyEquivalent: "")
                    hint.isEnabled = false
                    menu.addItem(hint)
                }
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

#if DEBUG
        menu.addItem(NSMenuItem.separator())
        let buildItem = NSMenuItem(title: "Build: \(BuildInfo.id)",
                                   action: nil, keyEquivalent: "")
        buildItem.isEnabled = false
        menu.addItem(buildItem)
#endif

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
        if let existing = session, existing.isConnecting {
            NSLog("Comprador: Ignoring attach — connection already in progress")
            return
        }
        if let existing = session, existing.isMounted {
            NSLog("Comprador: Reattach while unmount in flight — queuing (entry 19a)")
            existing.pendingAttach = device
            return
        }

        let newSession = DeviceSession(device: device)
        newSession.delegate = self
        newSession.isConnecting = true
        session = newSession
        updateIcon(state: .connecting)
        rebuildMenu()

        Task {
            await newSession.connect()
        }
    }

    private func handleDeviceDetached(_ device: USBDevice) {
        NSLog("Comprador: Device detached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X)",
              device.vendorID, device.productID)

        guard let active = session else {
            updateIcon(state: .idle)
            rebuildMenu()
            return
        }

        // Ignore spurious detach events during connection — USB re-enumeration
        // causes rapid detach/attach cycles when the phone switches to MTP mode
        if active.isConnecting {
            NSLog("Comprador: Ignoring detach — connection in progress (USB re-enumeration)")
            return
        }

        // Only tear down if we're actually mounted or the bridge is running
        guard active.isMounted || active.bridge?.isRunning == true else {
            session = nil
            updateIcon(state: .idle)
            rebuildMenu()
            return
        }

        Task {
            await active.teardown()
            await MainActor.run {
                let pending = active.pendingAttach
                session = nil
                updateIcon(state: .idle)
                rebuildMenu()
                if let queued = pending {
                    handleDeviceAttached(queued)
                }
            }
        }
    }

    @objc private func showInFinder() {
        guard let path = session?.mountPath else { return }
        NSWorkspace.shared.open(path)
    }

    @objc private func ejectDevice() {
        NSLog("Comprador: Eject requested")
        guard let active = session else { return }
        active.isConnecting = false
        active.pendingAttach = nil
        active.stopConnectTimer()
        Task {
            await active.teardown()
            await MainActor.run {
                session = nil
                updateIcon(state: .idle)
                rebuildMenu()
            }
        }
    }

    @objc private func quitApp() {
        Task {
            await teardownCurrentSession()
            await MainActor.run {
                NSApplication.shared.terminate(nil)
            }
        }
    }

    private func teardownCurrentSession() async {
        guard let active = session else { return }
        await active.teardown()
        session = nil
    }

#if DEBUG
    /// Fires a synthetic detach+reattach pair synchronously on the main thread,
    /// reproducing the entry 19a race: handleDeviceDetached queues a teardown Task
    /// and returns; handleDeviceAttached runs before that Task executes, sees
    /// isMounted == true, and should queue via pendingAttach rather than discarding.
    @objc private func syntheticFlutter() {
        guard let device = session?.device else { return }
        NSLog("Comprador: ⚡ synthetic flutter — firing detach+reattach on \(device.displayName)")
        handleDeviceDetached(device)
        handleDeviceAttached(device)
    }
#endif

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

// MARK: - DeviceSessionDelegate

extension AppDelegate: DeviceSessionDelegate {
    func deviceSessionDidChangeMenuStructure(_ session: DeviceSession) {
        rebuildMenu()
    }

    func deviceSessionDidMount(_ session: DeviceSession, mountedURL: URL) {
        updateIcon(state: .mounted)
        rebuildMenu()
        NSWorkspace.shared.open(mountedURL)
    }

    func deviceSession(_ session: DeviceSession, didFailWith error: Error) {
        updateIcon(state: .error)
        rebuildMenu()
    }
}
