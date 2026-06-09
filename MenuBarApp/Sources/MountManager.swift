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

    /// Unmounts the currently mounted volume.
    func unmount() async {
        guard let path = mountPath else { return }
        cprLog("Comprador: Unmounting %@", path.path)

        if let session = daSession,
           let disk = DADiskCreateFromVolumePath(kCFAllocatorDefault, session, path as CFURL) {
            DADiskUnmount(disk, DADiskUnmountOptions(kDADiskUnmountOptionDefault), { disk, dissenter, _ in
                if let dissenter = dissenter {
                    let status = DADissenterGetStatus(dissenter)
                    cprLog("Comprador: Clean unmount failed (status %d), forcing", status)
                    DADiskUnmount(disk, DADiskUnmountOptions(kDADiskUnmountOptionForce), nil, nil)
                } else {
                    cprLog("Comprador: Unmounted")
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

    /// Mounts the bridge NFS server using the unprivileged `mount(8)`
    /// path. macOS allows `mount -t nfs` from a non-root user when the
    /// target server is on localhost; the kernel adds `nodev,nosuid`
    /// flags automatically. Verified empirically 2026-05-08:
    ///
    ///     mount -t nfs -o port=N,mountport=N,nfsvers=3,nolocks,tcp \
    ///       localhost:/ /private/tmp/probe
    ///     # rc=0; mount table shows "mounted by terrace"
    ///
    /// This bypasses the privileged-helper layer entirely. The helper
    /// (and SMAppService.daemon registration) used to exist purely to
    /// launder root for `mount_nfs`; with this discovery they are
    /// vestigial for the NFS path and should not be invoked.
    ///
    /// The `/Volumes` directory is not user-writable on modern macOS
    /// (drwxr-xr-x root:wheel), so we mount under the user's
    /// Application Support tree instead. Finder still surfaces the
    /// mount as a Locations sidebar entry; the volume label shows as
    /// the mountpoint's parent name rather than `/Volumes/<phone>`,
    /// which is a cosmetic difference we accept.
    ///
    /// The `host` argument should be the mDNS-registered hostname the
    /// bridge advertises (typically `<DeviceName>.local`), not the
    /// bare loopback address. Finder's Locations sidebar displays the
    /// mount source's hostname for the volume label; mounting via
    /// `XQ-BT52.local:/` gives a much friendlier sidebar entry than
    /// `localhost:/`. Falls back to `localhost` if the bridge couldn't
    /// register a hostname.
    func mountNFS(host: String, port: Int, volumeName: String) async throws -> URL {
        let baseDir = try FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        ).appendingPathComponent("Comprador").appendingPathComponent("Volumes")
        try FileManager.default.createDirectory(at: baseDir,
                                                withIntermediateDirectories: true)
        let mountpoint = baseDir.appendingPathComponent(volumeName)
        // Mountpoint must exist and be empty; recreate if stale.
        try? FileManager.default.removeItem(at: mountpoint)
        try FileManager.default.createDirectory(at: mountpoint,
                                                withIntermediateDirectories: false)

        cprLog("Comprador: Mounting NFS on port %d at %@", port, mountpoint.path)

        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/sbin/mount")
        p.arguments = [
            "-t", "nfs",
            // vers=4.0: the bridge now serves Galatea's userspace NFSv4 server
            // (was willscott/go-nfs NFSv3 with nfsvers=3,nolocks). NFSv4 has
            // integrated locking, so nolocks is dropped.
            "-o", "vers=4.0,port=\(port),mountport=\(port),tcp",
            "\(host):/",
            mountpoint.path,
        ]
        let errPipe = Pipe()
        p.standardError = errPipe
        p.standardOutput = FileHandle.nullDevice
        try p.run()
        p.waitUntilExit()

        if p.terminationStatus != 0 {
            let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
            let errMsg = String(data: errData, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            cprLog("Comprador: mount(8) failed with status %d: %@",
                  p.terminationStatus, errMsg)
            // Best-effort cleanup of the empty mountpoint we created.
            try? FileManager.default.removeItem(at: mountpoint)
            throw MountError.mountFailed(p.terminationStatus)
        }

        self.mountPath = mountpoint
        cprLog("Comprador: NFS mounted at %@", mountpoint.path)
        return mountpoint
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
                cprLog("Comprador: cleaning up stale WebDAV mount %@", mp)
                forceUnmount(mp)
                continue
            }

            // NFS: mounts we created. Recognized by source prefix —
            // historically only 127.0.0.1:/ or localhost:/, but with
            // multi-device support each bridge advertises a per-device
            // .local hostname (e.g. "XQ-BT52.local:/", "Pixel-6.local:/")
            // so the cleanup needs to match those too. Defensive
            // narrowing: also require the mountpoint to live under our
            // Comprador/Volumes directory, so we never force-unmount a
            // user's unrelated localhost:/ NFS mount.
            if s.contains("(nfs") {
                let isOurSource = s.hasPrefix("127.0.0.1:/")
                    || s.hasPrefix("localhost:/")
                    || s.range(of: #"^[A-Za-z][A-Za-z0-9-]+\.local:/"#,
                               options: .regularExpression) != nil
                guard isOurSource else { continue }
                guard let onRange = s.range(of: " on "),
                      let parenRange = s.range(of: " (nfs") else { continue }
                let mp = String(s[onRange.upperBound..<parenRange.lowerBound])
                let isOurPath = mp.contains("/Comprador/Volumes/")
                    || mp.hasPrefix("/Volumes/")  // legacy helper path
                guard isOurPath else {
                    cprLog("Comprador: skipping NFS mount at %@ (source looks ours, path doesn't)", mp)
                    continue
                }
                cprLog("Comprador: cleaning up stale NFS mount %@", mp)
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
