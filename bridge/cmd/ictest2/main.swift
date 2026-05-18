import Foundation
import ImageCaptureCore
import CryptoKit

// ictest2 — Test 2 from docs/RESEARCH-IMAGECAPTURECORE.md
//
// Hypothesis: PTP-mode sequential reads via ImageCaptureCore sustain
// >= 10 MB/s and no chunk exceeds 2s latency.
//
// Compile:  make ictest2
// Run:      ./build/ictest2
//
// Pre-run:
//   - Pixel 6 (or other phone exposing PTP) connected, USB mode = PTP
//   - Image Capture.app open (so ptpcamerad is alive — coexistence sanity)
//   - A media file >= 50 MB on the phone (record a few minutes of video
//     if you don't already have one). If none found, the binary lists the
//     top-5 and exits.
//
// Exit codes:
//   0 — PASS  (throughput >= 10 MB/s, no chunk > 2s, byte count matches)
//   1 — read error mid-stream
//   2 — no device in 60s
//   4 — device removed during test
//   5 — completed but did not meet pass criteria
//   6 — no test file >= 50 MB on device

let CHUNK_SIZE: off_t = 4 * 1024 * 1024
let MIN_TEST_FILE_SIZE: off_t = 50 * 1024 * 1024
let CHUNK_TIMEOUT_S: TimeInterval = 30

class Test2: NSObject {
    let browser = ICDeviceBrowser()
    var camera: ICCameraDevice?
    var testFile: ICCameraFile?
    var enumerationTimeout: Timer?
    var chunkWatchdog: Timer?

    var startTime: Date?
    var nextOffset: off_t = 0
    var totalBytes: Int64 = 0
    var chunkCount: Int = 0
    var maxChunkLatencyMs: Double = 0
    var minChunkLatencyMs: Double = .infinity
    var chunkLatencies: [Double] = []
    var md5 = Insecure.MD5()
    var rssSamples: [Int] = []
    var pendingChunkStart: Date?
    var pendingChunkOffset: off_t = 0

    func run() {
        browser.delegate = self
        enumerationTimeout = Timer.scheduledTimer(withTimeInterval: 60, repeats: false) { _ in
            print("[TIMEOUT] No PTP-visible device in 60s. Pixel 6 in PTP mode?")
            exit(2)
        }
        browser.start()  // synchronous didAdd if device already connected → invalidates timer above
        probePtpcamerad(label: "startup")
        print("[info] ictest2 started — will read largest file >= \(MIN_TEST_FILE_SIZE / 1024 / 1024) MiB from first PTP-visible device")
        RunLoop.main.run()
    }

    func probePtpcamerad(label: String) {
        let t = Process()
        t.executableURL = URL(fileURLWithPath: "/usr/bin/pgrep")
        t.arguments = ["-lx", "ptpcamerad"]
        let pipe = Pipe()
        t.standardOutput = pipe
        try? t.run()
        t.waitUntilExit()
        let out = (String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        print("[ptpcamerad] \(label): \(out.isEmpty ? "NOT RUNNING" : out)")
    }

    func currentRSS() -> Int {
        let pid = ProcessInfo.processInfo.processIdentifier
        let t = Process()
        t.executableURL = URL(fileURLWithPath: "/bin/ps")
        t.arguments = ["-o", "rss=", "-p", String(pid)]
        let pipe = Pipe()
        t.standardOutput = pipe
        try? t.run()
        t.waitUntilExit()
        let out = (String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return Int(out) ?? 0
    }

    func openSession(on cam: ICCameraDevice) {
        camera = cam
        cam.delegate = self
        probePtpcamerad(label: "before requestOpenSession")
        print("[session] calling requestOpenSession...")
        let t0 = Date()
        cam.requestOpenSession(options: nil) { [weak self] error in
            let ms = Date().timeIntervalSince(t0) * 1000
            if let err = error {
                let ns = err as NSError
                print("[session] FAIL  domain=\(ns.domain)  code=\(ns.code)  msg=\"\(err.localizedDescription)\"")
                exit(1)
            }
            print("[session] open OK  elapsed=\(String(format: "%.0f", ms))ms")
            self?.probePtpcamerad(label: "after open")
            print("[catalog] waiting for deviceDidBecomeReady(withCompleteContentCatalog:)…")
        }
    }

    func pickTestFile(from cam: ICCameraDevice) {
        let items: [ICCameraItem] = cam.mediaFiles ?? []
        let files: [ICCameraFile] = items.compactMap { $0 as? ICCameraFile }
        print("[catalog] \(items.count) items, \(files.count) ICCameraFile")

        let bySize = files.sorted { $0.fileSize > $1.fileSize }
        for f in bySize.prefix(5) {
            print("[catalog]   '\(f.name ?? "")'  size=\(f.fileSize) (\(f.fileSize / 1024 / 1024) MiB)")
        }

        guard let largest = bySize.first, largest.fileSize >= MIN_TEST_FILE_SIZE else {
            print("[catalog] no file >= \(MIN_TEST_FILE_SIZE / 1024 / 1024) MiB. Record some video on the phone and re-run.")
            exit(6)
        }

        testFile = largest
        print("[read] target: '\(largest.name ?? "")'  size=\(largest.fileSize) bytes (\(largest.fileSize / 1024 / 1024) MiB)")
        print("[read] chunks of \(CHUNK_SIZE / 1024 / 1024) MiB (strict sequential)")
        startTime = Date()
        rssSamples.append(currentRSS())
        issueNextChunk()
    }

    func issueNextChunk() {
        guard let file = testFile, let cam = camera else { return }
        guard nextOffset < file.fileSize else {
            finishTest()
            return
        }

        let remaining = file.fileSize - nextOffset
        let len = min(remaining, CHUNK_SIZE)
        pendingChunkStart = Date()
        pendingChunkOffset = nextOffset

        chunkWatchdog?.invalidate()
        chunkWatchdog = Timer.scheduledTimer(withTimeInterval: CHUNK_TIMEOUT_S, repeats: false) { [weak self] _ in
            print("[read] CHUNK TIMEOUT at offset \(self?.pendingChunkOffset ?? 0) — no completion in \(CHUNK_TIMEOUT_S)s")
            self?.finishTest(forceFail: true)
        }

        cam.requestReadData(
            from: file,
            atOffset: nextOffset,
            length: len,
            readDelegate: self,
            didReadDataSelector: #selector(didReadData(_:fromFile:error:contextInfo:)),
            contextInfo: nil
        )
    }

    @objc func didReadData(_ data: Data?, fromFile file: ICCameraFile?, error: Error?, contextInfo: UnsafeMutableRawPointer?) {
        chunkWatchdog?.invalidate()
        let ms = (pendingChunkStart.map { Date().timeIntervalSince($0) } ?? 0) * 1000
        let chunkOffset = pendingChunkOffset

        if let err = error {
            print("[read] chunk@\(chunkOffset) ERROR: \(err.localizedDescription)")
            finishTest(forceFail: true)
            return
        }
        guard let data = data, !data.isEmpty else {
            print("[read] chunk@\(chunkOffset) empty data")
            finishTest(forceFail: true)
            return
        }

        totalBytes += Int64(data.count)
        chunkCount += 1
        maxChunkLatencyMs = max(maxChunkLatencyMs, ms)
        minChunkLatencyMs = min(minChunkLatencyMs, ms)
        chunkLatencies.append(ms)

        data.withUnsafeBytes { raw in
            if let base = raw.baseAddress {
                md5.update(bufferPointer: UnsafeRawBufferPointer(start: base, count: data.count))
            }
        }

        nextOffset += off_t(data.count)

        if chunkCount % 16 == 0 {
            let elapsedS = Date().timeIntervalSince(startTime!)
            let mbps = Double(totalBytes) / 1024 / 1024 / elapsedS
            let rss = currentRSS()
            rssSamples.append(rss)
            print(String(format: "[read] %4d chunks  %6.1f MiB  %.1f MB/s  rss=%d KiB  chunk_ms=%.0f",
                         chunkCount,
                         Double(totalBytes) / 1024 / 1024,
                         mbps,
                         rss,
                         ms))
        }

        issueNextChunk()
    }

    func finishTest(forceFail: Bool = false) {
        chunkWatchdog?.invalidate()
        guard let start = startTime else { exit(1) }
        let elapsedS = Date().timeIntervalSince(start)
        let mbps = Double(totalBytes) / 1024 / 1024 / elapsedS

        let sorted = chunkLatencies.sorted()
        let p50 = sorted.isEmpty ? 0 : sorted[sorted.count / 2]
        let p99 = sorted.isEmpty ? 0 : sorted[min(sorted.count - 1, Int(Double(sorted.count - 1) * 0.99))]

        let digest = md5.finalize()
        let md5Str = digest.map { String(format: "%02x", $0) }.joined()

        let rssMax = rssSamples.max() ?? 0
        let rssEnd = currentRSS()
        let usb = usbVersionDescription()

        print("")
        print("[result] -------------------------------------------------------")
        print("[result] file:           \(testFile?.name ?? "")")
        print("[result] expected:       \(testFile?.fileSize ?? 0) bytes")
        print("[result] read:           \(totalBytes) bytes (\(totalBytes / 1024 / 1024) MiB)")
        print("[result] chunks:         \(chunkCount) × \(CHUNK_SIZE / 1024 / 1024) MiB")
        print(String(format: "[result] elapsed:        %.1f s", elapsedS))
        print(String(format: "[result] throughput:     %.2f MB/s", mbps))
        print(String(format: "[result] chunk_ms:       min=%.0f p50=%.0f p99=%.0f max=%.0f",
                     minChunkLatencyMs, p50, p99, maxChunkLatencyMs))
        print("[result] md5:            \(md5Str)")
        print("[result] usb:            \(usb)")
        print("[result] rss:            end=\(rssEnd) KiB  peak=\(rssMax) KiB")
        print("[result] -------------------------------------------------------")

        let bytesOK  = totalBytes == (testFile?.fileSize ?? -1)
        let mbpsOK   = mbps >= 10.0
        let chunkOK  = maxChunkLatencyMs <= 2000
        let pass     = bytesOK && mbpsOK && chunkOK && !forceFail

        print("[verdict] \(pass ? "PASS" : "FAIL")  bytes=\(bytesOK ? "ok" : "MISMATCH")  thrpt=\(mbpsOK ? "ok" : "<10MB/s")  chunk=\(chunkOK ? "ok" : ">2s")")
        probePtpcamerad(label: "after read")

        print("[session] closing...")
        camera?.requestCloseSession(options: nil) { [weak self] err in
            if let e = err {
                print("[session] close FAIL: \(e.localizedDescription)")
            } else {
                print("[session] close OK")
            }
            self?.probePtpcamerad(label: "after close")
            print("[result] paste this output into RESEARCH-IMAGECAPTURECORE.md §Test 2 Results")
            exit(pass ? 0 : 5)
        }
    }

    func usbVersionDescription() -> String {
        let t = Process()
        t.executableURL = URL(fileURLWithPath: "/bin/sh")
        t.arguments = ["-c", "/usr/sbin/system_profiler SPUSBDataType 2>/dev/null | grep -B1 -A1 'Pixel\\|Xperia\\|Speed' | head -30"]
        let pipe = Pipe()
        t.standardOutput = pipe
        try? t.run()
        t.waitUntilExit()
        let out = (String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return out.isEmpty ? "(unknown)" : out.replacingOccurrences(of: "\n", with: " | ")
    }
}

extension Test2: ICDeviceBrowserDelegate {
    func deviceBrowser(_ browser: ICDeviceBrowser, didAdd device: ICDevice, moreComing: Bool) {
        let vid = device.usbVendorID
        let pid = device.usbProductID
        print("[browser] +device  name='\(device.name ?? "")'  vid=0x\(String(format: "%04X", vid))  pid=0x\(String(format: "%04X", pid))  uuid=\(device.uuidString ?? "")")
        guard camera == nil else { return }
        enumerationTimeout?.invalidate()
        guard let cam = device as? ICCameraDevice else {
            print("[browser] device not ICCameraDevice; read APIs unavailable")
            exit(1)
        }
        openSession(on: cam)
    }

    func deviceBrowser(_ browser: ICDeviceBrowser, didRemove device: ICDevice, moreGoing: Bool) {
        print("[browser] -device  name='\(device.name ?? "")'")
    }

    func deviceBrowserDidEnumerateLocalDevices(_ browser: ICDeviceBrowser) {
        print("[browser] initial enumeration complete")
    }
}

extension Test2: ICCameraDeviceDelegate {
    func cameraDevice(_ camera: ICCameraDevice, didAdd items: [ICCameraItem]) {}
    func cameraDevice(_ camera: ICCameraDevice, didRemove items: [ICCameraItem]) {}
    func cameraDevice(_ camera: ICCameraDevice, didReceiveThumbnail thumbnail: CGImage?, for item: ICCameraItem, error: Error?) {}
    func cameraDevice(_ camera: ICCameraDevice, didReceiveMetadata metadata: [AnyHashable : Any]?, for item: ICCameraItem, error: Error?) {}
    func cameraDevice(_ camera: ICCameraDevice, didRenameItems items: [ICCameraItem]) {}
    func cameraDeviceDidChangeCapability(_ camera: ICCameraDevice) {}
    func cameraDevice(_ camera: ICCameraDevice, didReceivePTPEvent eventData: Data) {}
    func cameraDeviceDidEnableAccessRestriction(_ device: ICDevice) {}
    func cameraDeviceDidRemoveAccessRestriction(_ device: ICDevice) {}

    func deviceDidBecomeReady(withCompleteContentCatalog device: ICCameraDevice) {
        print("[catalog] deviceDidBecomeReady(withCompleteContentCatalog:) fired")
        pickTestFile(from: device)
    }

    func didRemove(_ device: ICDevice) {
        print("[device] removed unexpectedly during test")
        exit(4)
    }

    func device(_ device: ICDevice, didOpenSessionWithError error: Error?) {
        if let e = error {
            print("[device:cb] didOpenSession error: \(e.localizedDescription)")
        }
    }

    func device(_ device: ICDevice, didCloseSessionWithError error: Error?) {
        if let e = error {
            print("[device:cb] didCloseSession error: \(e.localizedDescription)")
        }
    }
}

let test = Test2()
test.run()
