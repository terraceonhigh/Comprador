import Foundation
import ServiceManagement

/// Talks to the privileged comprador-helper daemon over a Unix socket.
///
/// The helper is registered via `SMAppService.daemon`. macOS prompts the
/// user once to approve a Login Item; after that it stays registered. The
/// daemon edits a managed block in `/etc/hosts` so the bridge can advertise
/// URLs like `http://Pixel-6:port/` and have NetFS mount them as
/// `/Volumes/Pixel-6` (no `.local` suffix).
enum HelperClient {
    static let socketPath = "/var/run/comprador-helper.sock"
    static let plistName = "com.comprador.helper.plist"

    enum HelperError: LocalizedError {
        case notRegistered
        case socketUnreachable(String)
        case ioError(String)
        case helperReturned(String)

        var errorDescription: String? {
            switch self {
            case .notRegistered:
                return "Comprador helper is not registered or has not been approved."
            case .socketUnreachable(let m):
                return "Cannot reach helper daemon: \(m)"
            case .ioError(let m):
                return "Helper IO error: \(m)"
            case .helperReturned(let m):
                return "Helper rejected request: \(m)"
            }
        }
    }

    // MARK: - Registration

    /// Whether the helper is approved and running.
    static var isEnabled: Bool {
        SMAppService.daemon(plistName: plistName).status == .enabled
    }

    /// Status descriptor for diagnostics.
    static var statusDescription: String {
        switch SMAppService.daemon(plistName: plistName).status {
        case .notRegistered:    return "notRegistered"
        case .enabled:          return "enabled"
        case .requiresApproval: return "requiresApproval"
        case .notFound:         return "notFound"
        @unknown default:       return "unknown"
        }
    }

    /// Register the helper. Triggers a macOS approval prompt the first time.
    /// Returns true on success or already-enabled state.
    @discardableResult
    static func register() -> Bool {
        let svc = SMAppService.daemon(plistName: plistName)
        if svc.status == .enabled { return true }
        do {
            try svc.register()
            cprLog("Comprador: helper register() OK, status now %@", statusDescription)
            return true
        } catch {
            cprLog("Comprador: helper register() failed: %@", error.localizedDescription)
            return false
        }
    }

    /// Remove the helper registration. Does not edit /etc/hosts.
    @discardableResult
    static func unregister() -> Bool {
        let svc = SMAppService.daemon(plistName: plistName)
        do {
            try svc.unregister()
            return true
        } catch {
            cprLog("Comprador: helper unregister() failed: %@", error.localizedDescription)
            return false
        }
    }

    // MARK: - Wire protocol

    static func ping() -> Bool {
        (try? send("PING")) == "OK"
    }

    /// Add `127.0.0.1 <name>` to the managed hosts block.
    /// `name` must be a single-label DNS name: `[A-Za-z][A-Za-z0-9-]{0,62}`.
    static func addHost(_ name: String) throws {
        let reply = try send("ADD \(name)")
        if reply != "OK" { throw HelperError.helperReturned(reply) }
    }

    /// Remove a previously-added host from the managed block.
    static func removeHost(_ name: String) throws {
        let reply = try send("REMOVE \(name)")
        if reply != "OK" { throw HelperError.helperReturned(reply) }
    }

    /// Drop the entire Comprador-managed block from /etc/hosts.
    static func clearHosts() throws {
        let reply = try send("CLEAR")
        if reply != "OK" { throw HelperError.helperReturned(reply) }
    }

    /// Ask the helper (running as root) to mount the bridge NFS server at
    /// /Volumes/<volumeName>.  Requires helper to be installed and enabled.
    static func mountNFS(port: Int, volumeName: String) throws {
        let reply = try send("MOUNT_NFS \(port) \(volumeName)")
        if reply != "OK" { throw HelperError.helperReturned(reply) }
    }

    /// Ask the helper to unmount /Volumes/<volumeName> and remove the dir.
    static func unmountNFS(volumeName: String) throws {
        let reply = try send("UNMOUNT_NFS \(volumeName)")
        if reply != "OK" { throw HelperError.helperReturned(reply) }
    }

    // MARK: - Internals

    /// Send one line, read one reply. Opens a fresh connection per call —
    /// the helper is on localhost via Unix socket, overhead is negligible.
    private static func send(_ command: String) throws -> String {
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathCapacity = MemoryLayout.size(ofValue: addr.sun_path)
        guard socketPath.utf8.count < pathCapacity else {
            throw HelperError.socketUnreachable("socket path too long")
        }
        socketPath.withCString { src in
            withUnsafeMutablePointer(to: &addr.sun_path) { sunPath in
                sunPath.withMemoryRebound(to: CChar.self, capacity: pathCapacity) { dst in
                    _ = strncpy(dst, src, pathCapacity - 1)
                }
            }
        }

        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        if fd < 0 {
            throw HelperError.socketUnreachable("socket(): \(String(cString: strerror(errno)))")
        }
        defer { close(fd) }

        let rc = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { addrPtr in
                connect(fd, addrPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if rc != 0 {
            throw HelperError.socketUnreachable("connect(): \(String(cString: strerror(errno)))")
        }

        let line = command + "\n"
        try line.withCString { ptr in
            let n = write(fd, ptr, strlen(ptr))
            if n < 0 {
                throw HelperError.ioError("write(): \(String(cString: strerror(errno)))")
            }
        }

        var buf = [UInt8](repeating: 0, count: 1024)
        let n = read(fd, &buf, buf.count - 1)
        if n < 0 {
            throw HelperError.ioError("read(): \(String(cString: strerror(errno)))")
        }
        let reply = String(bytes: buf.prefix(Int(n)), encoding: .utf8) ?? ""
        return reply.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
