import Foundation
import UserNotifications

/// Manages the lifecycle of the Go bridge process (WebDAV or NFS mode).
class BridgeProcess {
    private var process: Process?
    private(set) var port: Int?
    private(set) var host: String = "127.0.0.1"
    private(set) var deviceName: String?
    /// "nfs" when bridge was started with --nfs, "webdav" otherwise.
    private(set) var proto: String = "webdav"

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
                NSLog("Comprador: IOKit preflight OK (seized + re-enumerated 0x%04X:0x%04X)",
                      seizeForVendor, seizeForProduct)
                // Wait for USB to settle after re-enumeration. ~1s is
                // enough for IOKit to surface the new device handle;
                // shorter and we race the kernel binding before our
                // claim attempt.
                try? await Task.sleep(nanoseconds: 1_200_000_000)
            case .deviceNotFound:
                NSLog("Comprador: IOKit preflight skipped (device 0x%04X:0x%04X not found)",
                      seizeForVendor, seizeForProduct)
            case .pluginCreateFailed(let rc):
                NSLog("Comprador: IOKit preflight skipped (IOCreatePlugInInterfaceForService → 0x%X)",
                      UInt32(bitPattern: Int32(rc)))
            case .interfaceQueryFailed(let rc):
                NSLog("Comprador: IOKit preflight skipped (QueryInterface → %d)", rc)
            case .openSeizeFailed(let rc):
                NSLog("Comprador: IOKit preflight skipped (USBDeviceOpenSeize → 0x%X)",
                      UInt32(bitPattern: Int32(rc)))
            }
        }

        NSLog("Comprador: Starting bridge at %@", bridgePath)

        // Kill macOS processes that auto-claim MTP/PTP USB interfaces
        BridgeProcess.killCompetingProcesses()

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
            NSLog("Comprador bridge: %@", text.trimmingCharacters(in: .whitespacesAndNewlines))
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

        try p.run()
        self.process = p
        NSLog("Comprador: Bridge process started (PID %d)", p.processIdentifier)

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
        NSLog("Comprador: Bridge ready — proto=%@, addr=%@:%d, device: %@",
              self.proto, self.host, result.port, result.device ?? "unknown")
        return result.port
    }

    /// Stops the bridge process.
    func stop() {
        guard let p = process, p.isRunning else {
            process = nil
            port = nil
            deviceName = nil
            return
        }

        NSLog("Comprador: Stopping bridge (PID %d)", p.processIdentifier)
        p.terminate()

        // Give it a moment to exit cleanly, then force kill
        DispatchQueue.global().asyncAfter(deadline: .now() + 2) { [weak p] in
            if let p = p, p.isRunning {
                NSLog("Comprador: Force killing bridge")
                p.interrupt()
            }
        }

        process = nil
        port = nil
        deviceName = nil
        proto = "webdav"
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
                NSLog("Comprador: Killed %@", name)
            }
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
