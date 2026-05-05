import Foundation
import UserNotifications

/// Manages the lifecycle of the Go WebDAV bridge process.
class BridgeProcess {
    private var process: Process?
    private(set) var port: Int?
    private(set) var host: String = "127.0.0.1"
    private(set) var deviceName: String?

    /// Starts the bridge binary and waits for it to print PORT=N.
    /// Returns the port number on success.
    /// Throws if the bridge fails to start or doesn't respond within the timeout.
    func start() async throws -> Int {
        let bridgePath = findBridgeBinary()
        guard FileManager.default.fileExists(atPath: bridgePath) else {
            throw BridgeError.binaryNotFound(bridgePath)
        }

        NSLog("Comprador: Starting bridge at %@", bridgePath)

        // Kill macOS processes that auto-claim MTP/PTP USB interfaces
        BridgeProcess.killCompetingProcesses()

        let p = Process()
        p.executableURL = URL(fileURLWithPath: bridgePath)
        p.currentDirectoryURL = URL(fileURLWithPath: NSTemporaryDirectory())

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

        // Log stderr in background
        stderrPipe.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if !data.isEmpty, let line = String(data: data, encoding: .utf8) {
                NSLog("Comprador bridge: %@", line.trimmingCharacters(in: .whitespacesAndNewlines))
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
        NSLog("Comprador: Bridge ready on %@:%d, device: %@",
              self.host, result.port, result.device ?? "unknown")
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
    }

    var isRunning: Bool {
        process?.isRunning ?? false
    }

    // MARK: - Private

    /// Kills macOS processes that auto-claim MTP/PTP USB interfaces,
    /// preventing libusb from claiming them.
    static func killCompetingProcesses() {
        // Names vary across macOS versions; include both legacy and current.
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
        let handle = pipe.fileHandleForReading
        var accumulated = ""

        while port == nil || device == nil || host == nil {
            let data = handle.availableData
            guard !data.isEmpty else {
                if port != nil {
                    break // Got port; host/device may have arrived but stream closed
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
                }
            }

            // Brief wait for the rest of the metadata to arrive once we have port.
            if port != nil && (host == nil || device == nil) {
                try? await Task.sleep(nanoseconds: 150_000_000) // 150ms
            }
        }

        return BridgeStartupInfo(port: port!, host: host, device: device)
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
