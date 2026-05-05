import AppKit
import SwiftUI

/// One-shot welcome window shown on first launch.
///
/// Replaces two consecutive `NSAlert` modals (login item + helper) with a
/// single, calmer SwiftUI window. The helper has been moved out of first-
/// launch friction entirely — it's still discoverable via the menu bar
/// "Install helper…" item for users who want `/Volumes/Pixel-6` instead
/// of `/Volumes/Pixel-6.local`.
///
/// `present(onClose:)` shows the window; the controller dismisses itself
/// when the user clicks "Get Started" or closes the window. UserDefaults
/// `Comprador.didShowWelcome` is set on dismissal so the window only ever
/// appears once.
final class WelcomeWindowController: NSWindowController, NSWindowDelegate {
    static let didShowKey = "Comprador.didShowWelcome"

    private var onClose: (() -> Void)?
    private var previousActivationPolicy: NSApplication.ActivationPolicy = .accessory

    static func shouldPresent() -> Bool {
        !UserDefaults.standard.bool(forKey: didShowKey)
    }

    init() {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 560),
            styleMask: [.titled, .closable],
            backing: .buffered,
            defer: false
        )
        window.title = "Welcome to Comprador"
        window.center()
        window.isReleasedWhenClosed = false

        super.init(window: window)
        window.delegate = self

        let viewModel = WelcomeViewModel()
        let view = WelcomeView(viewModel: viewModel) { [weak self] in
            self?.close()
        }
        window.contentViewController = NSHostingController(rootView: view)
    }

    required init?(coder: NSCoder) { nil }

    /// Show the window. AppDelegate calls this once on first launch.
    func present(onClose: @escaping () -> Void) {
        self.onClose = onClose
        // LSUIElement = true means no dock icon by default. Temporarily flip
        // to .regular so the window is reachable / focusable, then restore
        // .accessory when it closes. Without this the window can sit behind
        // other apps with no way to switch to it.
        previousActivationPolicy = NSApp.activationPolicy()
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }

    func windowWillClose(_ notification: Notification) {
        UserDefaults.standard.set(true, forKey: WelcomeWindowController.didShowKey)
        NSApp.setActivationPolicy(previousActivationPolicy)
        onClose?()
        onClose = nil
    }
}

// MARK: - View Model

@MainActor
final class WelcomeViewModel: ObservableObject {
    @Published var loginItemEnabled: Bool = LoginItem.isEnabled

    func refresh() {
        loginItemEnabled = LoginItem.isEnabled
    }

    func enableLoginItem() {
        if !LoginItem.isEnabled {
            LoginItem.enable()
        }
        refresh()
    }
}

// MARK: - View

private struct WelcomeView: View {
    @ObservedObject var viewModel: WelcomeViewModel
    let onDismiss: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            hero
                .padding(.horizontal, 32)
                .padding(.top, 32)
                .padding(.bottom, 24)

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    howToConnect
                    setup
                }
                .padding(.horizontal, 32)
                .padding(.vertical, 24)
            }
            .frame(maxWidth: .infinity)

            Divider()

            HStack {
                Spacer()
                Button("Get Started", action: onDismiss)
                    .keyboardShortcut(.defaultAction)
                    .controlSize(.large)
            }
            .padding(.horizontal, 24)
            .padding(.vertical, 16)
        }
        .frame(width: 520, height: 560)
        .onAppear { viewModel.refresh() }
    }

    private var hero: some View {
        VStack(spacing: 10) {
            Image(systemName: "externaldrive.connected.to.line.below.fill")
                .font(.system(size: 44, weight: .regular))
                .foregroundStyle(.tint)
            Text("Welcome to Comprador")
                .font(.system(size: 22, weight: .semibold))
            Text("See Android phones and cameras in Finder.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    private var howToConnect: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("How to connect")
                .font(.headline)

            connectCard(
                icon: "iphone",
                title: "Android phones",
                steps: [
                    "Plug the phone into your Mac with a USB cable.",
                    "On the phone, pull down the notification shade and tap the USB notification → File Transfer.",
                ]
            )

            connectCard(
                icon: "camera",
                title: "Cameras (DSLR & mirrorless)",
                steps: [
                    "Plug the camera in and turn it on.",
                    "If the camera asks for a USB mode, choose PC / Computer (sometimes called MTP or PTP).",
                ]
            )

            Text("Once connected, your device shows up in the Finder sidebar under Locations.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .padding(.top, 4)
        }
    }

    private func connectCard(icon: String, title: String, steps: [String]) -> some View {
        HStack(alignment: .top, spacing: 14) {
            Image(systemName: icon)
                .font(.title2)
                .foregroundStyle(.tint)
                .frame(width: 28, alignment: .center)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 6) {
                Text(title).font(.body).fontWeight(.medium)
                ForEach(Array(steps.enumerated()), id: \.offset) { idx, step in
                    HStack(alignment: .top, spacing: 6) {
                        Text("\(idx + 1).")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .frame(width: 16, alignment: .trailing)
                        Text(step)
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(Color(nsColor: .controlBackgroundColor))
        )
    }

    private var setup: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Setup")
                .font(.headline)

            HStack(alignment: .top, spacing: 12) {
                Image(systemName: viewModel.loginItemEnabled
                        ? "checkmark.circle.fill"
                        : "circle")
                    .foregroundStyle(viewModel.loginItemEnabled ? .green : .secondary)
                    .font(.title3)
                    .padding(.top, 2)

                VStack(alignment: .leading, spacing: 2) {
                    Text("Start Comprador at login")
                        .font(.body)
                    Text("Recommended — your phone or camera will work the moment you plug it in, even after a restart.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }

                Spacer(minLength: 0)

                if viewModel.loginItemEnabled {
                    Text("Enabled")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    Button("Enable") { viewModel.enableLoginItem() }
                }
            }
            .padding(12)
            .background(
                RoundedRectangle(cornerRadius: 10)
                    .fill(Color(nsColor: .controlBackgroundColor))
            )
        }
    }
}
