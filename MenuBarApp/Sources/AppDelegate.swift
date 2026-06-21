import Cocoa
import ServiceManagement

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var deviceWatcher: DeviceWatcher!
    private var welcomeController: WelcomeWindowController?

    /// Active device sessions keyed by USB Location ID. PLAN-MULTI-DEVICE.md
    /// step 3: data structure widened from a single optional to a dictionary,
    /// even though step-3 behavior is still effectively single-device — the
    /// existing already-connecting / already-mounted guards in
    /// handleDeviceAttached keep the dict's size at 0 or 1 until step 5
    /// rewires the attach handler to genuinely accept multiple devices.
    private var sessions: [UInt32: DeviceSession] = [:]

    /// Devices ejected by the user, keyed by USB Location ID → eject time. An
    /// eject stops the bridge, which releases the USB interface; the kernel then
    /// re-binds and re-enumerates the (still-plugged) device, firing a
    /// detach→attach burst within ~1s. Without this, that burst auto-reconnects
    /// the device the user just ejected. We suppress reconnect for a short window
    /// after an eject — long enough to swallow the one-shot re-enumeration, short
    /// enough that a genuine later replug still reconnects. (Eject means "stop
    /// until I physically replug"; the burst isn't a replug.)
    private var recentlyEjected: [UInt32: Date] = [:]
    private let ejectReconnectSuppressWindow: TimeInterval = 6

    /// Convenience for step-3 callers that still assume single-device. The
    /// existing guards mean at most one session is active; returning the
    /// first dict value matches the old `session` semantics exactly.
    /// Step 5 will replace these call sites with per-device wiring (menu
    /// items knowing which session they target, etc.).
    private var currentSession: DeviceSession? {
        return sessions.values.first
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        cprLog("Comprador build: %@", BuildInfo.id)

        // Clear out any leftover webdav mounts from a prior session — otherwise
        // NetFS auto-suffixes today's mount as /Volumes/Pixel-6-1 and Finder
        // ends up showing duplicates.
        MountManager.cleanupStaleMounts()

        // Pre-emptively kill macOS processes that auto-claim USB
        // PTP/MTP interfaces (ptpcamerad, AMPDeviceDiscoveryAgent, …).
        // Without this, when N>=2 devices were plugged in *before* the
        // app launched, both DeviceSessions race to spawn bridges whose
        // IOKit-seize preflights collide on ptpcamerad's exclusive
        // hold — one wins (gets a clean re-enumeration), the other
        // gets kIOReturnExclusiveAccess, falls through to a bare
        // libusb_claim_interface, fails because the kernel's USB
        // Imaging Class driver has bound the interface. Empirically
        // reproducible 2026-05-17 with Xperia + Pixel pre-attached.
        // See MISTAKES.md entry 19b.
        //
        // Per-bridge killCompetingProcesses calls still fire on each
        // spawn (BridgeProcess.start) for the plug-after-launch path
        // where ptpcamerad may have re-spawned in the interim. The
        // app-startup call is the additional pre-emption for the
        // pre-attached case.
        BridgeProcess.killCompetingProcesses()

        setupStatusItem()
        setupDeviceWatcher()
        updateIcon(state: .idle)
        presentWelcomeIfNeeded()
    }

    func applicationWillTerminate(_ notification: Notification) {
        Task {
            await teardownAllSessions()
        }
    }

    // MARK: - Status Item

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        rebuildMenu()
    }

    private func rebuildMenu() {
        let menu = NSMenu()

        // Sort by locationID for stable ordering: the same two physical
        // ports will produce the same menu order every relaunch, so the
        // user doesn't see phones jump around the menu between sessions.
        let sortedSessions = sessions.values.sorted {
            $0.device.locationID < $1.device.locationID
        }

        if sortedSessions.isEmpty {
            let noDevice = NSMenuItem(title: "No device connected",
                                      action: nil, keyEquivalent: "")
            noDevice.isEnabled = false
            menu.addItem(noDevice)
        } else {
            for (idx, session) in sortedSessions.enumerated() {
                // Separator between device blocks. The first block runs
                // directly under the status item with no leading separator.
                if idx > 0 {
                    menu.addItem(NSMenuItem.separator())
                }

                let deviceItem = NSMenuItem(title: session.displayName,
                                            action: nil, keyEquivalent: "")
                deviceItem.isEnabled = false
                menu.addItem(deviceItem)

                if session.isMounted {
                    // Cmd-F / Cmd-E shortcuts only on the first mounted
                    // device. With N devices the shortcuts are
                    // necessarily ambiguous; the menu still works via
                    // pointer + click for the others.
                    let showItem = NSMenuItem(title: "Show in Finder",
                                              action: #selector(showInFinder(_:)),
                                              keyEquivalent: idx == 0 ? "f" : "")
                    showItem.target = self
                    showItem.representedObject = session
                    menu.addItem(showItem)

                    let ejectItem = NSMenuItem(title: "Eject \(session.displayName)",
                                               action: #selector(ejectDevice(_:)),
                                               keyEquivalent: idx == 0 ? "e" : "")
                    ejectItem.target = self
                    ejectItem.representedObject = session
                    menu.addItem(ejectItem)
#if DEBUG
                    let flutterItem = NSMenuItem(title: "⚡ Synthetic Flutter \(session.displayName)",
                                                 action: #selector(syntheticFlutter(_:)),
                                                 keyEquivalent: "")
                    flutterItem.target = self
                    flutterItem.representedObject = session
                    menu.addItem(flutterItem)
#endif
                } else if session.isConnecting {
                    let statusLine = NSMenuItem(title: session.connectingStatusTitle(),
                                                action: nil, keyEquivalent: "")
                    statusLine.isEnabled = false
                    menu.addItem(statusLine)
                    session.connectingStatusItem = statusLine

                    // (Removed the "Finder takes about 90 seconds to
                    // attach the volume" hint. It was correct for the
                    // legacy WebDAV path where NetFSMountURLSync dominated
                    // the cycle. The NFS path connects in a few seconds.
                    // DeviceSession hardcodes useNFS = true now so the
                    // hint was unconditionally misleading. The previous
                    // `session.bridgeProto != "nfs"` gate let it slip
                    // through during the pre-PROTO-parse window where
                    // BridgeProcess.proto still defaulted to "webdav".)
                }
            }
        }

        menu.addItem(NSMenuItem.separator())

        let loginItem = NSMenuItem(title: "Start at Login",
                                   action: #selector(toggleLoginItem),
                                   keyEquivalent: "")
        loginItem.state = LoginItem.isEnabled ? .on : .off
        menu.addItem(loginItem)

        menu.addItem(NSMenuItem(title: "Quit Comprador",
                                action: #selector(quitApp),
                                keyEquivalent: "q"))

        menu.addItem(NSMenuItem.separator())
        // Build identifier, clickable — copies BuildInfo.id to the
        // clipboard so the user (or the architect) can paste it into
        // a bug report or a journal note without retyping. Brief
        // "Copied!" flash on click as confirmation; reverts after ~1 s.
        //
        // Promoted out of #if DEBUG 2026-05-18 after the v0.3.3
        // retraction: when a production user hits a regression, the
        // first thing we need from them is "which exact build are you
        // running?" Burying this behind a debug flag means the answer
        // is unavailable to the people who most need to surface it.
        let buildItem = NSMenuItem(title: "Build: \(BuildInfo.id)",
                                   action: #selector(copyBuildID(_:)),
                                   keyEquivalent: "")
        buildItem.target = self
        buildItem.toolTip = "Click to copy the build identifier to the clipboard"
        menu.addItem(buildItem)

        statusItem.menu = menu
    }

    /// Copies the bridge/app build identifier to the system clipboard,
    /// then briefly flashes the menu item title to "Copied!" as
    /// confirmation. Reverts after ~1 second.
    @objc private func copyBuildID(_ sender: NSMenuItem) {
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(BuildInfo.id, forType: .string)

        let originalTitle = sender.title
        sender.title = "Copied! (\(BuildInfo.id))"
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            sender.title = originalTitle
        }
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
        cprLog("Comprador: Device attached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X, locID: 0x%08X)",
              device.vendorID, device.productID, device.locationID)

        // Suppress the post-eject re-enumeration burst: an eject releases the USB
        // interface, the kernel re-enumerates the still-plugged device, and that
        // attach would otherwise immediately re-mount what the user just ejected.
        // A genuine replug arrives after the window and reconnects normally.
        if let ejectedAt = recentlyEjected[device.locationID] {
            if Date().timeIntervalSince(ejectedAt) < ejectReconnectSuppressWindow {
                cprLog("Comprador: Ignoring attach — device was just ejected (replug to reconnect)")
                return
            }
            recentlyEjected.removeValue(forKey: device.locationID) // window passed — a real replug
        }

        // If a session for the same physical device (matched by USB
        // IOKit Location ID) already exists, treat the attach as a
        // reattach: in flight → ignore, mounted → queue pending teardown
        // (race-mitigation for entry 19a), otherwise fall through to
        // replace. Different-locationID attaches now proceed to create
        // a parallel DeviceSession — step 5 of PLAN-MULTI-DEVICE.md.
        // Each session has its own bridge (claimed via --device-loc-id),
        // its own MountManager, and its own mount point under
        // ~/Library/Application Support/Comprador/Volumes/<deviceName>.
        if let existing = sessions[device.locationID] {
            if existing.isConnecting {
                cprLog("Comprador: Ignoring attach — connection already in progress")
                return
            }
            if existing.isMounted {
                cprLog("Comprador: Reattach while unmount in flight — queuing (entry 19a)")
                existing.pendingAttach = device
                return
            }
            // Same locID, existing session is errored or torn down — fall
            // through to replace it with a fresh attempt.
            sessions.removeValue(forKey: device.locationID)
        }

        let newSession = DeviceSession(device: device)
        newSession.delegate = self
        newSession.isConnecting = true
        sessions[device.locationID] = newSession
        updateIcon(state: .connecting)
        rebuildMenu()

        Task {
            await newSession.connect()
        }
    }

    private func handleDeviceDetached(_ device: USBDevice) {
        cprLog("Comprador: Device detached — \(device.displayName) (vendor: 0x%04X, product: 0x%04X, locID: 0x%08X)",
              device.vendorID, device.productID, device.locationID)

        guard let active = sessions[device.locationID] else {
            // Detach for a device we never sessioned (or already cleared) —
            // if the dict is now empty, drop to idle.
            if sessions.isEmpty {
                updateIcon(state: .idle)
                rebuildMenu()
            }
            return
        }

        // Ignore spurious detach events during connection — USB re-enumeration
        // causes rapid detach/attach cycles when the phone switches to MTP mode
        if active.isConnecting {
            cprLog("Comprador: Ignoring detach — connection in progress (USB re-enumeration)")
            return
        }

        // Only tear down if we're actually mounted or the bridge is running
        guard active.isMounted || active.bridge?.isRunning == true else {
            sessions.removeValue(forKey: device.locationID)
            if sessions.isEmpty {
                updateIcon(state: .idle)
            }
            rebuildMenu()
            return
        }

        Task {
            await active.teardown()
            await MainActor.run {
                let pending = active.pendingAttach
                sessions.removeValue(forKey: device.locationID)
                if sessions.isEmpty {
                    updateIcon(state: .idle)
                }
                rebuildMenu()
                if let queued = pending {
                    handleDeviceAttached(queued)
                }
            }
        }
    }

    /// Returns the menu item's target session, falling back to
    /// currentSession for keyboard-shortcut invocations on the
    /// first-listed device.
    private func sessionFor(_ sender: Any?) -> DeviceSession? {
        if let item = sender as? NSMenuItem,
           let target = item.representedObject as? DeviceSession {
            return target
        }
        return currentSession
    }

    @objc private func showInFinder(_ sender: Any?) {
        guard let path = sessionFor(sender)?.mountPath else { return }
        NSWorkspace.shared.open(path)
    }

    @objc private func ejectDevice(_ sender: Any?) {
        guard let active = sessionFor(sender) else { return }
        cprLog("Comprador: Eject requested for \(active.displayName)")
        active.isConnecting = false
        active.pendingAttach = nil
        active.stopConnectTimer()
        let locID = active.device.locationID
        // Mark ejected so the re-enumeration burst that bridge.stop() triggers
        // doesn't auto-reconnect this device (see recentlyEjected).
        recentlyEjected[locID] = Date()
        Task {
            await active.teardown()
            await MainActor.run {
                sessions.removeValue(forKey: locID)
                if sessions.isEmpty {
                    updateIcon(state: .idle)
                }
                rebuildMenu()
            }
        }
    }

    @objc private func quitApp() {
        Task {
            await teardownAllSessions()
            await MainActor.run {
                NSApplication.shared.terminate(nil)
            }
        }
    }

    private func teardownAllSessions() async {
        // Snapshot keys + sessions so we don't mutate while iterating.
        let snapshot = Array(sessions.values)
        for s in snapshot {
            await s.teardown()
        }
        sessions.removeAll()
    }

#if DEBUG
    /// Fires a synthetic detach+reattach pair synchronously on the main thread,
    /// reproducing the entry 19a race: handleDeviceDetached queues a teardown Task
    /// and returns; handleDeviceAttached runs before that Task executes, sees
    /// isMounted == true, and should queue via pendingAttach rather than discarding.
    @objc private func syntheticFlutter(_ sender: Any?) {
        guard let device = sessionFor(sender)?.device else { return }
        cprLog("Comprador: ⚡ synthetic flutter — firing detach+reattach on \(device.displayName)")
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
