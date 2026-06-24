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
///   - `bridge`, `mountManager`
///   - `isConnecting`, `pendingAttach`
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

    // Per-device subprocess. Starts nil; populated during connect(); cleared by
    // teardown().
    private(set) var bridge: BridgeProcess?

    // Connect-phase flags. `isConnecting` is the lock that suppresses
    // duplicate attach events for the same physical session. `pendingAttach`
    // queues a reattach event that landed while teardown was in flight
    // (MISTAKES.md entry 19a).
    var isConnecting = false
    var pendingAttach: USBDevice?

    // Self-healing (G1b): if the bridge dies unexpectedly after a successful
    // mount, recover by unmounting the stale volume and reconnecting. Bounded so
    // a bridge that crashes on every start doesn't spin forever — `maxRecoveries`
    // attempts within `recoveryWindow`, after which we give up and surface.
    private var recoveryAttempts = 0
    private var lastRecoveryAt: Date?
    private let maxRecoveries = 3
    private let recoveryWindow: TimeInterval = 120
    // Read by AppDelegate's external-unmount observer to skip the app's own
    // eject (which unmounts first, firing didUnmount while teardown is in flight).
    private(set) var tearingDown = false

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
        cprLog("Comprador: [status] %@", s)
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

        // Fail-fast: one attempt, no retry. The previous [0, 3, 5]-second
        // retry cycle had a hidden cost — each attempt does a fresh
        // IOKit USBDeviceReEnumerate + libusb_claim_interface cycle, and
        // each such cycle on a phone whose USB state is already
        // unhappy can push it further into a stuck non-MTP mode
        // (Pixel: 0x4EE1 → 0x4EE8 observed 2026-05-17). One clean try,
        // then surface the unplug-and-replug notification — letting the
        // user reset the phone\'s USB state with a physical action is
        // far more reliable than us banging on it from the host side.
        // The DeviceWatcher will see the resulting detach/attach and
        // trigger a fresh attempt automatically.
        let retryDelays: [UInt64] = [0]

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
                    // Same weak-var-into-concurrent-closure issue as
                    // onUnexpectedExit below: DispatchQueue.main.async takes a
                    // @Sendable closure, so re-capture self immutably in its
                    // capture list rather than referencing the enclosing weak var.
                    DispatchQueue.main.async { [weak self] in self?.setConnectStatus(msg) }
                }

                let useNFS = true
                let port = try await bp.start(
                    useNFS: useNFS,
                    seizeForVendor: device.vendorID,
                    seizeForProduct: device.productID,
                    locationID: device.locationID
                )

                // Eject sets isConnecting=false to cancel. The bridge spawn is a
                // long await (up to ~20s of IOKit seize + libmtp open); if the
                // user ejected during it, bail before mounting — otherwise we'd
                // re-mount a device they intentionally ejected (the connect/
                // teardown race). teardown() concurrently stops the bridge too;
                // both are idempotent.
                guard isConnecting else {
                    cprLog("Comprador: connect cancelled during bridge spawn — aborting before mount")
                    bp.stop()
                    self.bridge = nil
                    return
                }

                let displayName = bp.deviceName ?? device.displayName

                await MainActor.run { setConnectStatus("Mounting…") }

                // NFSv4 (Galatea) is the only serving mode since v0.4.0.
                let volName = AppDelegate.sanitizeHostname(displayName)
                guard !volName.isEmpty else {
                    throw MountError.mountFailed(-1)
                }
                let mountedURL = try await mountManager.mountNFS(
                    host: bp.host,
                    port: port,
                    volumeName: volName
                )

                // Ejected during the mount itself? Unmount what we just mounted
                // and bail before announcing it to the delegate (which would put
                // it in Finder's sidebar).
                guard isConnecting else {
                    cprLog("Comprador: connect cancelled during mount — unmounting, not announcing")
                    await mountManager.unmount()
                    bp.stop()
                    self.bridge = nil
                    return
                }

                // Supervise the live bridge: if it dies now (panic, libmtp
                // fault, anything that isn't our own stop()), self-heal instead
                // of leaving Finder pointed at a dead mount (G1b).
                bp.onUnexpectedExit = { [weak self] in
                    // Bind a strong, immutable `self` before the Task. A weak
                    // capture is modeled as a *var* (ARC can zero it across a
                    // concurrency boundary), so referencing it inside the
                    // @Sendable Task is "reference to captured var 'self' in
                    // concurrently-executing code" — a hard error on the macos-14
                    // runner's Swift toolchain (it only warns locally). guard-let
                    // makes the capture immutable.
                    guard let self else { return }
                    Task { await self.handleUnexpectedBridgeExit() }
                }

                await MainActor.run {
                    cprLog("Comprador: Device mounted as volume")
                    stopConnectTimer()
                    connectStatus = ""
                    isConnecting = false
                    delegate?.deviceSessionDidMount(self, mountedURL: mountedURL)
                }
                return
            } catch let bridgeErr as BridgeError where bridgeErr == .timeout {
                cprLog("Comprador: Bridge timeout — prompting user")
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
                    cprLog("Comprador: Attempt %d failed (%@), retrying...", attempt + 1, err.localizedDescription)
                } else {
                    cprLog("Comprador: All attempts failed — %@", err.localizedDescription)
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
        // Suppress self-healing: a teardown (eject/detach) is an intentional
        // stop. bridge.stop() also flags it, but set this first to close the
        // race where the bridge died from USB loss just as teardown begins.
        tearingDown = true
        if mountManager.isMounted {
            await mountManager.unmount()
        }
        bridge?.stop()
        bridge = nil
        tearingDown = false
    }

    // MARK: - Self-healing (G1b)

    /// Invoked (on an arbitrary queue, via BridgeProcess.onUnexpectedExit) when
    /// the bridge dies after a successful mount. Unmounts the now-dead volume
    /// and reconnects, bounded so a bridge that crashes on every start can't
    /// spin forever. A non-technical user can't debug a frozen mount, so the
    /// app makes a stuck bridge blip-and-recover instead of hang.
    func handleUnexpectedBridgeExit() async {
        // Don't fight an in-flight connect or teardown.
        if isConnecting || tearingDown { return }

        let now = Date()
        if let last = lastRecoveryAt, now.timeIntervalSince(last) > recoveryWindow {
            recoveryAttempts = 0 // crashes are far apart — treat as fresh
        }
        recoveryAttempts += 1
        lastRecoveryAt = now

        if recoveryAttempts > maxRecoveries {
            cprLog("Comprador: bridge died %d× within %.0fs — giving up auto-recovery",
                   recoveryAttempts, recoveryWindow)
            await teardown()
            await MainActor.run {
                delegate?.deviceSession(self, didFailWith: MountError.mountFailed(-1))
            }
            return
        }

        cprLog("Comprador: bridge died — self-healing (attempt %d/%d)",
               recoveryAttempts, maxRecoveries)

        // Drop the stale mount + dead bridge handle, then reconnect. connect()
        // spawns a fresh BridgeProcess and remounts; its guard reads isConnecting.
        if mountManager.isMounted {
            await mountManager.unmount()
        }
        bridge = nil
        await MainActor.run {
            isConnecting = true
            setConnectStatus("Reconnecting…")
            delegate?.deviceSessionDidChangeMenuStructure(self)
        }
        await connect()
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
