import Cocoa

/// One connected USB device's connect/mount lifecycle and state.
///
/// Encapsulates everything that was previously a loose `private var` on
/// `AppDelegate` and represented "the one connected device." This is the
/// PLAN-MULTI-DEVICE.md step 2 refactor: a no-op rewrite for single-device,
/// then step 3 introduces `[DeviceID: DeviceSession]` so N of these can
/// coexist.
///
/// Lifecycle:
///   - init with a USBDevice (does not start the bridge yet).
///   - `connect()` runs the bridge spawn + NFS/WebDAV mount sequence,
///     calling back to the delegate at status transitions.
///   - `teardown()` is the inverse — stops resume companion, unmounts,
///     stops the bridge, removes any registered hostname.
///
/// Per-device state that *was* on AppDelegate, now here:
///   - `bridge`, `mountManager`, `resumeCompanion`
///   - `isConnecting`, `pendingAttach`, `registeredHostname`
///   - `connectStatus`, `connectStartedAt`, `connectTimer`,
///     `connectingStatusItem`
///
/// State that stays on AppDelegate (because it's app-singleton, not
/// per-device): `statusItem`, `deviceWatcher`, `welcomeController`,
/// helper status, login-item state, icon state machine.
final class DeviceSession {
    weak var delegate: DeviceSessionDelegate?

    let device: USBDevice
    let mountManager = MountManager()

    // Per-device subprocess + companion. Both start nil; populated during
    // connect(); cleared by teardown().
    private(set) var bridge: BridgeProcess?
    private(set) var resumeCompanion: ResumeCompanion?
    private(set) var registeredHostname: String?

    // Connect-phase flags. `isConnecting` is the lock that suppresses
    // duplicate attach events for the same physical session. `pendingAttach`
    // queues a reattach event that landed while teardown was in flight
    // (MISTAKES.md entry 19a).
    var isConnecting = false
    var pendingAttach: USBDevice?

    // Status-text fields driving the connecting line in the menu.
    // `connectingStatusItem` is a weak handle to the NSMenuItem AppDelegate
    // owns — we mutate it in place so the elapsed counter advances while
    // the menu is open (replacing the whole menu would not redraw an
    // already-open one).
    private(set) var connectStatus: String = ""
    private(set) var connectStartedAt: Date?
    private var connectTimer: Timer?
    weak var connectingStatusItem: NSMenuItem?

    init(device: USBDevice) {
        self.device = device
    }

    // MARK: - Display helpers

    var displayName: String {
        bridge?.deviceName ?? device.displayName
    }

    var isMounted: Bool {
        mountManager.isMounted
    }

    var mountPath: URL? {
        mountManager.mountPath
    }

    var bridgeProto: String? {
        bridge?.proto
    }

    /// Builds the connecting status line text. Pulled out of rebuildMenu so the
    /// timer tick can update the visible NSMenuItem in place — replacing the
    /// whole menu via rebuildMenu() does not redraw an already-open menu, so
    /// the elapsed counter only ticked when the user closed and reopened.
    func connectingStatusTitle() -> String {
        let phase = connectStatus.isEmpty ? "Connecting…" : connectStatus
        guard let start = connectStartedAt else { return phase }
        let s = Int(-start.timeIntervalSinceNow)
        return String(format: "%@  %d:%02d", phase, s / 60, s % 60)
    }

    // MARK: - Status text plumbing

    func setConnectStatus(_ s: String) {
        NSLog("Comprador: [status] %@", s)
        let isFirstCall = connectStartedAt == nil
        connectStatus = s
        if isFirstCall {
            connectStartedAt = Date()
            let t = Timer(timeInterval: 1.0, repeats: true) { [weak self] _ in
                self?.tickConnectingStatus()
            }
            RunLoop.main.add(t, forMode: .common)
            RunLoop.main.add(t, forMode: .eventTracking)
            connectTimer = t
            delegate?.deviceSessionDidChangeMenuStructure(self)
        } else {
            connectingStatusItem?.title = connectingStatusTitle()
        }
    }

    func stopConnectTimer() {
        connectTimer?.invalidate()
        connectTimer = nil
        connectStartedAt = nil
    }

    private func tickConnectingStatus() {
        guard isConnecting, let item = connectingStatusItem else { return }
        item.title = connectingStatusTitle()
    }

    // MARK: - Connect

    /// Drives the full connect sequence: bridge spawn, mount, status updates.
    /// Returns nothing; signals success/failure via the delegate.
    func connect() async {
        // Wait for USB to fully settle — the phone does multiple
        // detach/reattach cycles when switching to MTP mode.
        try? await Task.sleep(nanoseconds: 5_000_000_000)

        // Retry with increasing delay
        let retryDelays: [UInt64] = [0, 3, 5]

        for (attempt, delaySec) in retryDelays.enumerated() {
            guard isConnecting else { return } // cancelled

            if delaySec > 0 {
                try? await Task.sleep(nanoseconds: delaySec * 1_000_000_000)
            }

            let bp = BridgeProcess()
            self.bridge = bp

            do {
                await MainActor.run { setConnectStatus("Starting bridge…") }

                // Surface key bridge-side milestones in the menu while connecting.
                bp.onStatusLine = { [weak self] msg in
                    DispatchQueue.main.async { self?.setConnectStatus(msg) }
                }

                let useNFS = true
                let port = try await bp.start(
                    useNFS: useNFS,
                    seizeForVendor: device.vendorID,
                    seizeForProduct: device.productID,
                    locationID: device.locationID
                )

                let displayName = bp.deviceName ?? device.displayName

                // If the helper is approved, override the bridge's
                // mDNS-derived `.local` hostname with a clean single-label
                // name pulled from /etc/hosts.
                var mountHost = bp.host
                if HelperClient.isEnabled,
                   let cleanLabel = registerCleanHostname(named: displayName) {
                    mountHost = cleanLabel
                }

                await MainActor.run { setConnectStatus("Mounting…") }

                let mountedURL: URL
                if bp.proto == "nfs" {
                    let volName = AppDelegate.sanitizeHostname(displayName)
                    guard !volName.isEmpty else {
                        throw MountError.mountFailed(-1)
                    }
                    mountedURL = try await mountManager.mountNFS(
                        host: bp.host,
                        port: port,
                        volumeName: volName
                    )
                } else {
                    mountedURL = try await mountManager.mount(
                        host: mountHost,
                        port: port,
                        displayName: displayName
                    )
                    let bridgeURL = URL(string: "http://\(bp.host):\(port)/")!
                    let companion = ResumeCompanion(bridgeURL: bridgeURL)
                    companion.start()
                    self.resumeCompanion = companion
                }

                await MainActor.run {
                    NSLog("Comprador: Device mounted as volume")
                    stopConnectTimer()
                    connectStatus = ""
                    isConnecting = false
                    delegate?.deviceSessionDidMount(self, mountedURL: mountedURL)
                }
                return
            } catch let bridgeErr as BridgeError where bridgeErr == .timeout {
                NSLog("Comprador: Bridge timeout — prompting user")
                BridgeProcess.postFileTransferNotification()
                bp.stop()
                self.bridge = nil
                await MainActor.run {
                    stopConnectTimer()
                    connectStatus = ""
                    isConnecting = false
                    delegate?.deviceSession(self, didFailWith: bridgeErr)
                }
                return
            } catch let err {
                bp.stop()
                self.bridge = nil
                if attempt < retryDelays.count - 1 {
                    NSLog("Comprador: Attempt %d failed (%@), retrying...", attempt + 1, err.localizedDescription)
                } else {
                    NSLog("Comprador: All attempts failed — %@", err.localizedDescription)
                    BridgeProcess.postFileTransferNotification()
                    await MainActor.run {
                        stopConnectTimer()
                        connectStatus = ""
                        isConnecting = false
                        delegate?.deviceSession(self, didFailWith: err)
                    }
                }
            }
        }
    }

    // MARK: - Teardown

    func teardown() async {
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

    // MARK: - Hostname helper

    /// Sanitises a friendly device name into a DNS label and asks the
    /// privileged helper to point it at 127.0.0.1 in /etc/hosts. Returns
    /// the hostname on success, or nil on any failure (caller falls back
    /// to whatever the bridge advertised — typically mDNS).
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
}

/// Callbacks DeviceSession needs back to its owner (AppDelegate).
/// Kept narrow: anything the session itself can compute or hold lives
/// on the session; the delegate only handles AppDelegate-owned UI
/// (icon state, menu rebuild, system-level Finder open).
protocol DeviceSessionDelegate: AnyObject {
    /// The session's structural state changed enough that the menu
    /// should be rebuilt (transitioning idle → connecting → mounted,
    /// or status text being shown for the first time in a cycle).
    func deviceSessionDidChangeMenuStructure(_ session: DeviceSession)

    /// The mount succeeded; pass the volume URL so the delegate can
    /// open Finder, switch the icon to mounted, and rebuild the menu.
    func deviceSessionDidMount(_ session: DeviceSession, mountedURL: URL)

    /// The connect sequence ended in failure (timeout, all retries
    /// exhausted, etc.). Delegate should switch the icon to error
    /// and rebuild the menu.
    func deviceSession(_ session: DeviceSession, didFailWith error: Error)
}
