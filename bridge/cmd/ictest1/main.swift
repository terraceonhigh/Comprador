import Foundation
import ImageCaptureCore

// ictest1 — Test 1 from docs/RESEARCH-IMAGECAPTURECORE.md
//
// Hypothesis: ICDevice.requestOpenSession succeeds while ptpcamerad is alive.
// Paste the full output into the Results section of RESEARCH-IMAGECAPTURECORE.md,
// then write a Conclusion. That doc is the deliverable; this binary is the probe.
//
// Compile:
//   make ictest1
// or:
//   swiftc -framework ImageCaptureCore -framework Foundation \
//     bridge/cmd/ictest1/main.swift -o build/ictest1
//
// Pre-run checklist:
//   1. Open Image Capture.app so ptpcamerad is alive before this binary starts.
//   2. Phone plugged in, USB mode = File Transfer (MTP). Run the binary.
//   3. If no device found in 60s, switch phone to Photo Transfer (PTP) and re-run.
//      MTP vs PTP visibility in ICDeviceBrowser is itself a meaningful result.
//
// Exit codes:
//   0 — PASS (session opened and closed cleanly)
//   1 — FAIL (requestOpenSession returned an error — hypothesis falsified)
//   2 — TIMEOUT (no matching device in 60s — phone not connected or wrong mode)
//   3 — TIMEOUT (session completion never fired in 30s — delegate/TCC issue)
//   4 — Device removed unexpectedly during session

class Test1: NSObject {
    let browser = ICDeviceBrowser()
    var targetDevice: ICDevice?
    var enumerationTimeout: Timer?

    func run() {
        browser.delegate = self
        enumerationTimeout = Timer.scheduledTimer(withTimeInterval: 60, repeats: false) { _ in
            print("")
            print("[TIMEOUT] No matching device found in 60s.")
            print("[TIMEOUT] If phone was in File Transfer (MTP) mode: switch to Photo Transfer (PTP) and re-run.")
            print("[TIMEOUT] MTP-mode non-visibility in ICDeviceBrowser is itself a result — record it.")
            exit(2)
        }
        browser.start()
        probePtpcamerad(label: "startup")
        print("[info] ICDeviceBrowser started — will open session on first device that appears")
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
        print("[ptpcamerad] \(label): \(out.isEmpty ? "NOT RUNNING (open Image Capture.app to start it)" : out)")
    }

    func openSession(on device: ICDevice) {
        probePtpcamerad(label: "before requestOpenSession")
        print("[session] calling requestOpenSession(options: nil)...")

        let watchdog = Timer.scheduledTimer(withTimeInterval: 30, repeats: false) { _ in
            print("")
            print("[TIMEOUT] requestOpenSession completion did not fire in 30s.")
            print("[TIMEOUT] Possible causes:")
            print("[TIMEOUT]   • TCC denial — check System Settings → Privacy → Media & Apple Devices")
            print("[TIMEOUT]   • Delegate/retain issue (unlikely for this binary)")
            print("[TIMEOUT]   • Framework deadlock")
            print("[TIMEOUT] This is exit 3 — distinct from a clean framework refusal (exit 1).")
            exit(3)
        }

        let t0 = Date()
        device.requestOpenSession(options: nil) { [weak self] error in
            watchdog.invalidate()
            let ms = Date().timeIntervalSince(t0) * 1000

            if let err = error {
                let ns = err as NSError
                print("[session] FAIL  domain=\(ns.domain)  code=\(ns.code)  msg=\"\(err.localizedDescription)\"  elapsed=\(String(format: "%.0f", ms))ms")
                self?.probePtpcamerad(label: "after FAIL")
                print("[result] Hypothesis FALSIFIED — framework refused concurrent session")
                exit(1)
            }

            print("[session] PASS  error=nil  elapsed=\(String(format: "%.0f", ms))ms")
            self?.probePtpcamerad(label: "after open")
            print("")
            print("[manual] -------------------------------------------------------")
            print("[manual] Session is open. Switch to Image Capture.app and:")
            print("[manual]   • Confirm the device still appears in the sidebar")
            print("[manual]   • Try clicking a thumbnail or importing a photo")
            print("[manual]   • Note: responsive / frozen / device disappeared")
            print("[manual] Press Enter here when done.")
            print("[manual] -------------------------------------------------------")

            DispatchQueue.global().async { [weak self] in
                _ = readLine()
                DispatchQueue.main.async {
                    print("[session] requesting close...")
                    device.requestCloseSession(options: nil) { [weak self] closeErr in
                        if let e = closeErr {
                            let ns = e as NSError
                            print("[session] close FAIL  domain=\(ns.domain)  code=\(ns.code)")
                        } else {
                            print("[session] close OK")
                        }
                        self?.probePtpcamerad(label: "after close")
                        print("")
                        print("[result] Hypothesis SUPPORTED — paste this output into RESEARCH-IMAGECAPTURECORE.md §Test 1 Results")
                        exit(0)
                    }
                }
            }
        }
    }
}

extension Test1: ICDeviceBrowserDelegate {
    func deviceBrowser(_ browser: ICDeviceBrowser, didAdd device: ICDevice, moreComing: Bool) {
        let vid = device.usbVendorID
        let pid = device.usbProductID
        print("[browser] +device  name='\(device.name ?? "")'  vid=0x\(String(format: "%04X", vid))  pid=0x\(String(format: "%04X", pid))  transport=\(String(describing: device.transportType))  uuid=\(device.uuidString ?? "")")

        guard targetDevice == nil else {
            print("[browser]   (already have target, ignoring)")
            return
        }
        targetDevice = device
        enumerationTimeout?.invalidate()
        enumerationTimeout = nil
        print("[target] using: '\(device.name ?? "")'  class=\(type(of: device))")
        openSession(on: device)
    }

    func deviceBrowser(_ browser: ICDeviceBrowser, didRemove device: ICDevice, moreGoing: Bool) {
        print("[browser] -device  name='\(device.name ?? "")'  moreGoing=\(moreGoing)")
    }

    func deviceBrowserDidEnumerateLocalDevices(_ browser: ICDeviceBrowser) {
        print("[browser] initial enumeration complete")
    }
}

let test = Test1()
test.run()
