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
