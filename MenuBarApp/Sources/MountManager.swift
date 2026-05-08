import Foundation
import NetFS
import DiskArbitration

/// Manages mounting and unmounting WebDAV volumes.
///
/// Uses NetFS because /Volumes isn't user-writable on modern macOS, and NetFS
/// goes through a privileged helper to create the mount point. The downside is
/// NetFS auto-names the volume from the URL host (so it shows as "127.0.0.1"
/// in Finder's sidebar). A custom name would require either an /etc/hosts
/// entry (needs sudo) or registering a per-device mDNS .local hostname.
class MountManager {
    private(set) var mountPath: URL?
    private var daSession: DASession?

    init() {
        daSession = DASessionCreate(kCFAllocatorDefault)
        if let session = daSession {
            DASessionScheduleWithRunLoop(session, CFRunLoopGetMain(), CFRunLoopMode.defaultMode.rawValue)
        }
    }

    deinit {
        if let session = daSession {
            DASessionUnscheduleFromRunLoop(session, CFRunLoopGetMain(), CFRunLoopMode.defaultMode.rawValue)
        }
    }

    /// Mounts a WebDAV URL and returns the mount path.
    /// `host` is the URL host the bridge advertises (typically a per-device
    /// `<name>.local` registered via mDNS); NetFS auto-names the volume from it.
    func mount(host: String, port: Int, displayName: String) async throws -> URL {
        let serverURL = URL(string: "http://\(host):\(port)/")! as CFURL
        let mountDir = URL(fileURLWithPath: "/Volumes") as CFURL

        NSLog("Comprador: Mounting WebDAV from %@:%d", host, port)

        return try await withCheckedThrowingContinuation { continuation in
            var mountPoints: Unmanaged<CFArray>?

            let openOptions: NSMutableDictionary = [
                kNAUIOptionKey: kNAUIOptionNoUI,
            ]

            // Empty mountOptions on purpose. We tried passing
            // `kNetFSMountFlagsKey: MNT_SYNCHRONOUS` to suppress webdavfs's
            // writeseq path (the source of -36 truncation on large Finder
            // drags); statfs(2) on the resulting mount confirmed the flag
            // is silently filtered out by webdavfs's mnt_flag handling.
            // Don't try this again — webdavfs has no exposed knob to
            // disable writeseq. See TODO.md "Make Finder error -36
            // disappear for very large files" → option 2 for the path
            // forward.
            let mountOptions = NSMutableDictionary()

            let rc = NetFSMountURLSync(
                serverURL,
                mountDir,
                "" as CFString,
                "" as CFString,
                openOptions,
                mountOptions,
                &mountPoints
            )

            if rc != 0 {
                NSLog("Comprador: Mount failed with error %d", rc)
                continuation.resume(throwing: MountError.mountFailed(rc))
                return
            }

            let resolvedPath: URL
            if let points = mountPoints?.takeRetainedValue() as? [String],
               let first = points.first {
                resolvedPath = URL(fileURLWithPath: first)
            } else {
                resolvedPath = URL(fileURLWithPath: "/Volumes/\(host)")
            }

            self.mountPath = resolvedPath
            NSLog("Comprador: Mounted at %@", resolvedPath.path)
            continuation.resume(returning: resolvedPath)
        }
    }

    /// Unmounts the currently mounted volume.
    func unmount() async {
        guard let path = mountPath else { return }
        NSLog("Comprador: Unmounting %@", path.path)

        if let session = daSession,
           let disk = DADiskCreateFromVolumePath(kCFAllocatorDefault, session, path as CFURL) {
            DADiskUnmount(disk, DADiskUnmountOptions(kDADiskUnmountOptionDefault), { disk, dissenter, _ in
                if let dissenter = dissenter {
                    let status = DADissenterGetStatus(dissenter)
                    NSLog("Comprador: Clean unmount failed (status %d), forcing", status)
                    DADiskUnmount(disk, DADiskUnmountOptions(kDADiskUnmountOptionForce), nil, nil)
                } else {
                    NSLog("Comprador: Unmounted")
                }
            }, nil)
        } else {
            // Fallback: shell out to umount
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/sbin/umount")
            p.arguments = [path.path]
            p.standardOutput = FileHandle.nullDevice
            p.standardError = FileHandle.nullDevice
            try? p.run()
            p.waitUntilExit()
            if p.terminationStatus != 0 {
                let fp = Process()
                fp.executableURL = URL(fileURLWithPath: "/sbin/umount")
                fp.arguments = ["-f", path.path]
                fp.standardOutput = FileHandle.nullDevice
                fp.standardError = FileHandle.nullDevice
                try? fp.run()
                fp.waitUntilExit()
            }
        }

        mountPath = nil
    }

    var isMounted: Bool {
        mountPath != nil
    }

    // MARK: - NFS

    /// Mounts the bridge NFS server via the privileged helper.
    /// The helper execs `mount_nfs` as root; this call returns once the
    /// mount is confirmed at /Volumes/<volumeName>.
    func mountNFS(port: Int, volumeName: String) async throws -> URL {
        NSLog("Comprador: Mounting NFS on port %d as /Volumes/%@", port, volumeName)
        try HelperClient.mountNFS(port: port, volumeName: volumeName)
        let mountedURL = URL(fileURLWithPath: "/Volumes/\(volumeName)")
        self.mountPath = mountedURL
        NSLog("Comprador: NFS mounted at %@", mountedURL.path)
        return mountedURL
    }

    /// Force-unmount every WebDAV volume that points at 127.0.0.1 (or a
    /// hostname we've registered). Called at app startup to clear out
    /// stale mounts left behind by a previous crash, kill, or restart —
    /// otherwise NetFS picks `/Volumes/Pixel-6-1`, then `-2`, etc.
    static func cleanupStaleMounts() {
        let pipe = Pipe()
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/sbin/mount")
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        do { try p.run() } catch { return }
        p.waitUntilExit()

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        guard let output = String(data: data, encoding: .utf8) else { return }

        for line in output.split(separator: "\n") {
            let s = String(line)

            // WebDAV: "http://host:port/ on /Volumes/foo (webdav, ...)"
            if s.contains("(webdav") {
                let isOurs = s.contains("://127.0.0.1:")
                    || s.contains("://localhost:")
                    || s.range(of: #"://[A-Za-z][A-Za-z0-9-]+(\.local)?:[0-9]+/"#, options: .regularExpression) != nil
                guard isOurs else { continue }
                guard let onRange = s.range(of: " on "),
                      let parenRange = s.range(of: " (webdav") else { continue }
                let mp = String(s[onRange.upperBound..<parenRange.lowerBound])
                NSLog("Comprador: cleaning up stale WebDAV mount %@", mp)
                forceUnmount(mp)
                continue
            }

            // NFS: "127.0.0.1:/ on /Volumes/foo (nfs, ...)"
            if s.contains("(nfs") && s.hasPrefix("127.0.0.1:/") {
                guard let onRange = s.range(of: " on "),
                      let parenRange = s.range(of: " (nfs") else { continue }
                let mp = String(s[onRange.upperBound..<parenRange.lowerBound])
                NSLog("Comprador: cleaning up stale NFS mount %@", mp)
                forceUnmount(mp)
            }
        }
    }

    private static func forceUnmount(_ mountPoint: String) {
        let u = Process()
        u.executableURL = URL(fileURLWithPath: "/sbin/umount")
        u.arguments = ["-f", mountPoint]
        u.standardOutput = FileHandle.nullDevice
        u.standardError = FileHandle.nullDevice
        try? u.run()
        u.waitUntilExit()
    }
}

enum MountError: LocalizedError {
    case mountFailed(Int32)

    var errorDescription: String? {
        switch self {
        case .mountFailed(let code):
            return "WebDAV mount failed with error code \(code)"
        }
    }
}
