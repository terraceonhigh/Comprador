import Foundation
import ServiceManagement

/// Wraps SMAppService.mainApp for "Start at Login" support.
///
/// Requires macOS 13+ (matches our LSMinimumSystemVersion). On first
/// registration the user is prompted in System Settings → General →
/// Login Items.
enum LoginItem {
    /// Whether the app is currently registered to launch at login.
    static var isEnabled: Bool {
        SMAppService.mainApp.status == .enabled
    }

    /// Whether the user explicitly disabled the app in System Settings.
    /// In this state, register() will not re-enable it; the user must
    /// flip the toggle themselves.
    static var requiresApproval: Bool {
        SMAppService.mainApp.status == .requiresApproval
    }

    /// Register to launch at login. Returns true on success.
    @discardableResult
    static func enable() -> Bool {
        do {
            try SMAppService.mainApp.register()
            NSLog("AndroidFS: Registered as login item")
            return true
        } catch {
            NSLog("AndroidFS: Failed to register login item: %@", error.localizedDescription)
            return false
        }
    }

    /// Unregister from launching at login. Returns true on success.
    @discardableResult
    static func disable() -> Bool {
        do {
            try SMAppService.mainApp.unregister()
            NSLog("AndroidFS: Unregistered as login item")
            return true
        } catch {
            NSLog("AndroidFS: Failed to unregister login item: %@", error.localizedDescription)
            return false
        }
    }

    /// Toggle the login item state.
    @discardableResult
    static func toggle() -> Bool {
        isEnabled ? disable() : enable()
    }
}
