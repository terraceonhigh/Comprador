// Log.swift — public-marked unified logging for Comprador.
//
// macOS's unified logging system defaults format-string arguments to
// `<private>` redaction. NSLog respects that default and never marks
// its content as public, which means `log stream --predicate process ==
// "Comprador"` shows the timing of NSLog calls but redacts every
// argument as `(Foundation) <private>`. This made the 2026-05-18
// cascade diagnostic harder than it needed to be — we had 69 NSLog
// calls firing during a critical 60-sec window and couldn't read any
// of their content.
//
// The fix: route everything through `cprLog`, which wraps `os_log`
// with `%{public}@`. Existing NSLog usage stays a near-mechanical
// rename (`NSLog(` → `cprLog(`); format strings continue to work.
//
// Filter the resulting log stream by subsystem:
//
//   log stream --predicate 'subsystem == "com.comprador.app"'

import Foundation
import os.log

/// Unified-logging handle for the Comprador process. The subsystem
/// matches CFBundleIdentifier; the category is broad ("default")
/// because most of our NSLog usage isn't sub-categorized. Future
/// refactors can split into per-category Loggers (bridge, mount,
/// device, helper, etc.) if we want finer-grained filtering.
private let comprardorOSLog = OSLog(subsystem: "com.comprador.app",
                                    category: "default")

/// Drop-in replacement for NSLog that marks the rendered format
/// string as public, so `log stream` shows the content rather than
/// `<private>`.
///
/// Used identically:
///
///   cprLog("Comprador: Bridge ready — port=%d device=%@", port, name)
///
/// Internally calls `String(format:arguments:)` to render the
/// arguments, then ships the result through `os_log` with
/// `%{public}@`. This is one allocation per call beyond what NSLog
/// did, which is acceptable for a process that logs at human-readable
/// rates (not per-RPC).
public func cprLog(_ format: String, _ args: CVarArg...) {
    let rendered = String(format: format, arguments: args)
    os_log("%{public}@", log: comprardorOSLog, type: .default, rendered)
}
