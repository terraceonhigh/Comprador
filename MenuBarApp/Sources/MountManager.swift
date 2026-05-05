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

        NSLog("AndroidFS: Mounting WebDAV from %@:%d", host, port)

        return try await withCheckedThrowingContinuation { continuation in
            var mountPoints: Unmanaged<CFArray>?

            let openOptions: NSMutableDictionary = [
                kNAUIOptionKey: kNAUIOptionNoUI,
            ]
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
                NSLog("AndroidFS: Mount failed with error %d", rc)
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
            NSLog("AndroidFS: Mounted at %@", resolvedPath.path)
            continuation.resume(returning: resolvedPath)
        }
    }

    /// Unmounts the currently mounted volume.
    func unmount() async {
        guard let path = mountPath else { return }
        NSLog("AndroidFS: Unmounting %@", path.path)

        if let session = daSession,
           let disk = DADiskCreateFromVolumePath(kCFAllocatorDefault, session, path as CFURL) {
            DADiskUnmount(disk, DADiskUnmountOptions(kDADiskUnmountOptionDefault), { disk, dissenter, _ in
                if let dissenter = dissenter {
                    let status = DADissenterGetStatus(dissenter)
                    NSLog("AndroidFS: Clean unmount failed (status %d), forcing", status)
                    DADiskUnmount(disk, DADiskUnmountOptions(kDADiskUnmountOptionForce), nil, nil)
                } else {
                    NSLog("AndroidFS: Unmounted")
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
            // Format: "http://host:port/ on /Volumes/foo (webdav, ...)"
            let s = String(line)
            guard s.contains("(webdav") else { continue }
            // Match anything that looks like our bridge: localhost-ish URLs.
            let isOurs = s.contains("://127.0.0.1:")
                || s.contains("://localhost:")
                || s.range(of: #"://[A-Za-z][A-Za-z0-9-]+(\.local)?:[0-9]+/"#, options: .regularExpression) != nil
            guard isOurs else { continue }
            guard let onRange = s.range(of: " on "),
                  let parenRange = s.range(of: " (webdav") else { continue }
            let mp = String(s[onRange.upperBound..<parenRange.lowerBound])
            NSLog("AndroidFS: cleaning up stale mount %@", mp)

            let u = Process()
            u.executableURL = URL(fileURLWithPath: "/sbin/umount")
            u.arguments = ["-f", mp]
            u.standardOutput = FileHandle.nullDevice
            u.standardError = FileHandle.nullDevice
            try? u.run()
            u.waitUntilExit()
        }
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
