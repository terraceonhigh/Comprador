import Foundation
import UserNotifications

/// Manages the lifecycle of the Go bridge process (WebDAV or NFS mode).
class BridgeProcess {
    private var process: Process?
    private(set) var port: Int?
    private(set) var host: String = "127.0.0.1"
    private(set) var deviceName: String?
    /// Serving protocol reported by the bridge. NFSv4 is the only mode since
    /// v0.4.0 (WebDAV retired); kept as a field because the bridge still emits
    /// PROTO=.
    private(set) var proto: String = "nfs"

    /// Set true by stop() so the terminationHandler can tell an intentional
    /// shutdown (teardown/eject) from an unexpected death (crash, panic, libmtp
    /// fault). Only the latter triggers onUnexpectedExit.
    private var intentionalStop = false

    /// Called (on an arbitrary thread) when the bridge exits WITHOUT us having
    /// called stop() — i.e. it died. The session wires this to its self-healing
    /// path (unmount the now-dead volume, re-spawn, remount). nil until a mount
    /// succeeds, so a death during the initial spawn flows through start()'s
    /// throw instead.
    var onUnexpectedExit: (() -> Void)?

    /// Called (on an arbitrary thread) with a short human-readable status
    /// string whenever a key milestone is observed in bridge stderr output.
    var onStatusLine: ((String) -> Void)?

    /// Starts the bridge binary and waits for it to print PORT=N.
    /// Returns the port number on success.
    /// Throws if the bridge fails to start or doesn't respond within the timeout.
    ///
    /// If `useNFS` is true the bridge is started with `--nfs` and serves
    /// NFSv3 instead of WebDAV.  Caller should mount via
    /// `HelperClient.mountNFS(port:volumeName:)`.
    ///
    /// If `seizeForVendor` and `seizeForProduct` are non-zero, an IOKit
    /// preflight runs first: seizes exclusive access to the device,
    /// re-enumerates it (USB-level detach/reattach, equivalent to physical
    /// replug), and releases. This is the only reliable way to break the
    /// macOS kernel driver's bind on a class-6 PTP interface so libusb's
    /// claim_interface can succeed on the bridge's first attempt.
    ///
    /// If `locationID` is non-zero, it is passed to the bridge as
    /// `--device-loc-id=<hex>` so the bridge claims the specific USB
    /// device matching that IOKit Location ID rather than the first
    /// detected MTP device. Required for multi-device operation: with
    /// two or more phones plugged in, the bridge would otherwise be
    /// non-deterministic about which one it claims. See
    /// docs/PLAN-MULTI-DEVICE.md §6 option A.
    func start(useNFS: Bool = false,
               seizeForVendor: UInt16 = 0,
               seizeForProduct: UInt16 = 0,
               locationID: UInt32 = 0) async throws -> Int {
        let bridgePath = findBridgeBinary()
        guard FileManager.default.fileExists(atPath: bridgePath) else {
            throw BridgeError.binaryNotFound(bridgePath)
        }

        // Kill macOS processes that auto-claim MTP/PTP USB interfaces
        // BEFORE the IOKit seize preflight, not after. ptpcamerad holds
        // each phone's USB Imaging Class interface in exclusive-access
        // mode; an immediately-following USBDeviceOpenSeize() will
        // return kIOReturnExclusiveAccess (0xE00002C5) and the seize
        // (and the kernel-binding break that depends on it) will
        // silently fail. Empirically reproducible with N>=2 devices
        // pre-attached: the first session's seize fires against a
        // still-alive ptpcamerad and fails; the second session's seize
        // benefits from the first session's killCompetingProcesses
        // call and succeeds. Swapping the order makes both seizes run
        // against an already-dead ptpcamerad. See MISTAKES.md entry
        // 19b for the full trace.
        // Reap a same-device bridge orphaned by a prior crashed/killed app
        // instance before we try to claim the interface — an orphan contends for
        // it and can make our seize fail "interface locked" (G2). Safe here:
        // this instance hasn't spawned its own bridge yet.
        BridgeProcess.killOrphanedBridges(locationID: locationID)

        BridgeProcess.killCompetingProcesses()

        // IOKit preflight: force a software replug so the bridge sees a
        // fresh, kernel-unclaimed device. This sequence (seize → reset →
        // release) is what physical unplug+replug does at the hardware
        // level — except we don't need the user to touch the cable.
        if seizeForVendor != 0 && seizeForProduct != 0 {
            let result = USBSeizer.seizeAndReset(
                vendorID: seizeForVendor,
                productID: seizeForProduct
            )
            switch result {
            case .success:
                cprLog("Comprador: IOKit preflight OK (seized + re-enumerated 0x%04X:0x%04X)",
                      seizeForVendor, seizeForProduct)
                // Wait for USB to settle after re-enumeration. ~1s is
                // enough for IOKit to surface the new device handle;
                // shorter and we race the kernel binding before our
                // claim attempt.
                try? await Task.sleep(nanoseconds: 1_200_000_000)
            case .deviceNotFound:
                cprLog("Comprador: IOKit preflight skipped (device 0x%04X:0x%04X not found)",
                      seizeForVendor, seizeForProduct)
            case .pluginCreateFailed(let rc):
                cprLog("Comprador: IOKit preflight skipped (IOCreatePlugInInterfaceForService → 0x%X)",
                      UInt32(bitPattern: Int32(rc)))
            case .interfaceQueryFailed(let rc):
                cprLog("Comprador: IOKit preflight skipped (QueryInterface → %d)", rc)
            case .openSeizeFailed(let rc):
                cprLog("Comprador: IOKit preflight skipped (USBDeviceOpenSeize → 0x%X)",
                      UInt32(bitPattern: Int32(rc)))
            }
        }

        cprLog("Comprador: Starting bridge at %@", bridgePath)

        let p = Process()
        p.executableURL = URL(fileURLWithPath: bridgePath)
        p.currentDirectoryURL = URL(fileURLWithPath: NSTemporaryDirectory())
        var args: [String] = []
        if useNFS {
            args.append("--nfs")
        }
        if locationID != 0 {
            args.append(String(format: "--device-loc-id=0x%08x", locationID))
        }
        p.arguments = args

        // Ensure libmtp can be found when launched from app bundle
        var env = ProcessInfo.processInfo.environment
        let homebrewLib = "/opt/homebrew/lib"
        if let existing = env["DYLD_LIBRARY_PATH"] {
            env["DYLD_LIBRARY_PATH"] = "\(homebrewLib):\(existing)"
        } else {
            env["DYLD_LIBRARY_PATH"] = homebrewLib
        }
        p.environment = env

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        p.standardOutput = stdoutPipe
        p.standardError = stderrPipe

        // Log stderr in background and extract status milestones for the menu.
        stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            cprLog("Comprador bridge: %@", text.trimmingCharacters(in: .whitespacesAndNewlines))
            guard let cb = self?.onStatusLine else { return }
            for line in text.components(separatedBy: .newlines) {
                if line.contains("Open attempt") {
                    cb("Waiting for USB interface…")
                } else if line.contains("Connected to: ") {
                    let name = line.components(separatedBy: "Connected to: ").last?
                        .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    if !name.isEmpty { cb("Connected to \(name)") }
                } else if line.contains("Registered mDNS hostname:") || line.contains("Registered hostname") {
                    cb("Registering hostname…")
                }
            }
        }

        // Detect unexpected death. Fires on an arbitrary queue when the process
        // exits; if we didn't ask for it (intentionalStop), notify the session
        // so it can self-heal. Set before run() so a fast exit can't slip past.
        p.terminationHandler = { [weak self] proc in
            guard let self = self else { return }
            if self.intentionalStop { return }
            cprLog("Comprador: bridge exited unexpectedly (status %d) — notifying session",
                   proc.terminationStatus)
            self.onUnexpectedExit?()
        }

        try p.run()
        self.process = p
        cprLog("Comprador: Bridge process started (PID %d)", p.processIdentifier)

        // Read PORT=, HOST=, and DEVICE= from stdout with timeout
        let result = try await withThrowingTaskGroup(of: BridgeStartupInfo.self) { group in
            group.addTask {
                try await self.readPortFromStdout(stdoutPipe)
            }
            group.addTask {
                try await Task.sleep(nanoseconds: 20_000_000_000) // 20 seconds
                throw BridgeError.timeout
            }

            let result = try await group.next()!
            group.cancelAll()
            return result
        }

        self.port = result.port
        self.host = result.host ?? "127.0.0.1"
        self.deviceName = result.device
        self.proto = result.proto ?? "webdav"
        cprLog("Comprador: Bridge ready — proto=%@, addr=%@:%d, device: %@",
              self.proto, self.host, result.port, result.device ?? "unknown")
        return result.port
    }

    /// Stops the bridge process. Marks the exit intentional so the
    /// terminationHandler does not mistake it for a crash and trigger recovery.
    func stop() {
        intentionalStop = true
        guard let p = process, p.isRunning else {
            process = nil
            port = nil
            deviceName = nil
            return
        }

        cprLog("Comprador: Stopping bridge (PID %d)", p.processIdentifier)
        p.terminate()

        // Give it a moment to exit cleanly, then force kill
        DispatchQueue.global().asyncAfter(deadline: .now() + 2) { [weak p] in
            if let p = p, p.isRunning {
                cprLog("Comprador: Force killing bridge")
                p.interrupt()
            }
        }

        process = nil
        port = nil
        deviceName = nil
        proto = "nfs"
    }

    var isRunning: Bool {
        process?.isRunning ?? false
    }

    // MARK: - Private

    /// Best-effort kill of macOS daemons that may be holding the USB
    /// interface. launchd respawns them within ~60ms, but the kill
    /// briefly disrupts their open file descriptors and *occasionally*
    /// helps when the holder isn't using exclusive access.
    ///
    /// We tried `launchctl bootout gui/<UID>/com.apple.ptpcamerad` to
    /// actually unload the LaunchAgent rather than just kill it; SIP
    /// forbids that on Apple-shipped agents in /System/Library, even
    /// for root. See TODO.md for the full diagnosis. The remaining
    /// best-effort recovery path is physical unplug+replug, which the
    /// failure-notification copy already tells the user about.
    static func killCompetingProcesses() {
        let processNames = [
            "ptpcamerad", "PTPCamera",
            "AMPDeviceDiscoveryAgent", "AMPDevicesAgent",
            "MTPCamera",
        ]
        for name in processNames {
            let task = Process()
            task.executableURL = URL(fileURLWithPath: "/usr/bin/killall")
            task.arguments = ["-9", name]
            task.standardOutput = FileHandle.nullDevice
            task.standardError = FileHandle.nullDevice
            try? task.run()
            task.waitUntilExit()
            if task.terminationStatus == 0 {
                cprLog("Comprador: Killed %@", name)
            }
        }
    }

    /// Reap orphaned bridge subprocesses left by a prior app instance that was
    /// killed without a clean teardown — macOS doesn't process-group-kill a
    /// child when its parent dies, so the bridge outlives the app. An orphan
    /// still holds/contends for the phone's USB interface, so a fresh seize can
    /// fail "interface locked" against *it*, not only against ptpcamerad. Scoped
    /// to the same --device-loc-id so a second device's bridge is untouched
    /// (multi-device-safe). Called before the seize, when this instance hasn't
    /// spawned its own bridge yet, so it can't kill our own.
    static func killOrphanedBridges(locationID: UInt32) {
        guard locationID != 0 else { return } // can't scope safely without it
        let pattern = String(
            format: "Comprador.app/Contents/Resources/bridge.*--device-loc-id=0x%08x",
            locationID)
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/bin/pkill")
        task.arguments = ["-9", "-f", pattern]
        task.standardOutput = FileHandle.nullDevice
        task.standardError = FileHandle.nullDevice
        try? task.run()
        task.waitUntilExit()
        if task.terminationStatus == 0 {
            cprLog("Comprador: reaped orphaned bridge(s) for loc 0x%08x", locationID)
        }
    }

    private func findBridgeBinary() -> String {
        // First check app bundle Resources
        if let bundlePath = Bundle.main.path(forResource: "bridge", ofType: nil) {
            return bundlePath
        }
        // Fallback: development path (bridge binary next to the app)
        let devPath = Bundle.main.bundlePath
            .components(separatedBy: "/")
            .dropLast(1) // Remove .app
            .joined(separator: "/") + "/../../../build/bridge"
        let resolved = (devPath as NSString).standardizingPath
        if FileManager.default.fileExists(atPath: resolved) {
            return resolved
        }
        // Last resort: build output in project root
        return "build/bridge"
    }

    private func readPortFromStdout(_ pipe: Pipe) async throws -> BridgeStartupInfo {
        var port: Int?
        var host: String?
        var device: String?
        var proto: String?
        let handle = pipe.fileHandleForReading
        var accumulated = ""

        // NFS bridge prints PORT, PROTO, DEVICE (no HOST).
        // WebDAV bridge prints PORT, HOST, DEVICE (no PROTO).
        // We're done when we have port + device + (host || proto).
        func isComplete() -> Bool {
            guard let _ = port, let _ = device else { return false }
            return host != nil || proto != nil
        }

        while !isComplete() {
            let data = handle.availableData
            guard !data.isEmpty else {
                if port != nil {
                    break // stream closed after port — accept what we have
                }
                throw BridgeError.exitedEarly
            }

            if let output = String(data: data, encoding: .utf8) {
                accumulated += output
                for line in accumulated.components(separatedBy: .newlines) {
                    if line.hasPrefix("PORT="), let p = Int(line.dropFirst(5)) {
                        port = p
                    }
                    if line.hasPrefix("HOST=") {
                        let h = String(line.dropFirst(5))
                        if !h.isEmpty { host = h }
                    }
                    if line.hasPrefix("DEVICE=") {
                        let name = String(line.dropFirst(7))
                        if !name.isEmpty { device = name }
                    }
                    if line.hasPrefix("PROTO=") {
                        let p = String(line.dropFirst(6))
                        if !p.isEmpty { proto = p }
                    }
                }
            }

            // Brief wait once port is in — give the rest of the metadata 150ms to arrive.
            if port != nil && !isComplete() {
                try? await Task.sleep(nanoseconds: 150_000_000)
            }
        }

        return BridgeStartupInfo(port: port!, host: host, device: device, proto: proto)
    }

    /// Posts a notification telling the user how to recover from a failed
    /// connection. Two recovery paths are bundled because we can't tell
    /// from the bridge side which one applies:
    ///
    ///   1. User hasn't selected File Transfer yet → tap the USB
    ///      notification on the phone.
    ///   2. User has selected File Transfer but the USB descriptor is
    ///      stale (some Pixel/Android builds don't re-enumerate when the
    ///      mode changes — the phone's UI says MTP but the OS still sees
    ///      it as PTP) → unplug and replug to force fresh enumeration.
    static func postFileTransferNotification() {
        let center = UNUserNotificationCenter.current()
        center.requestAuthorization(options: [.alert]) { granted, _ in
            guard granted else { return }

            let content = UNMutableNotificationContent()
            content.title = "Check your phone"
            content.body = "Select \"File Transfer\" on your phone. If it's already selected, unplug and replug to refresh the connection."
            content.sound = .default

            let request = UNNotificationRequest(
                identifier: "file-transfer-prompt",
                content: content,
                trigger: nil
            )
            center.add(request)
        }
    }
}

struct BridgeStartupInfo {
    let port: Int
    let host: String?
    let device: String?
    let proto: String?
}

enum BridgeError: LocalizedError, Equatable {
    static func == (lhs: BridgeError, rhs: BridgeError) -> Bool {
        switch (lhs, rhs) {
        case (.timeout, .timeout): return true
        case (.exitedEarly, .exitedEarly): return true
        case (.binaryNotFound(let a), .binaryNotFound(let b)): return a == b
        default: return false
        }
    }

    case binaryNotFound(String)
    case timeout
    case exitedEarly

    var errorDescription: String? {
        switch self {
        case .binaryNotFound(let path):
            return "Bridge binary not found at \(path)"
        case .timeout:
            return "Bridge did not respond within 15 seconds (File Transfer mode not selected?)"
        case .exitedEarly:
            return "Bridge process exited before reporting port"
        }
    }
}
