<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# USBDriver — Comprador's DriverKit extension

This directory will hold the USB DriverKit extension (dext) sources
once the Xcode target is scaffolded. **It is currently empty by
design** — we cannot meaningfully scaffold a dext target without:

1. Apple Developer Program enrollment **active** (done — Team ID
   `5875SC35WL`).
2. App IDs `com.comprador.app` and `com.comprador.app.USBDriver`
   registered at developer.apple.com → Identifiers, with the
   System Extension capability ticked on the host App ID
   (self-service, no Apple review).
3. `com.apple.developer.driverkit.transport.usb` entitlement
   approved for the dext (`com.comprador.app.USBDriver`). This
   one **does** need Apple review — typically 2–6 weeks.

See [../docs/ENTITLEMENT-REQUEST.md](../docs/ENTITLEMENT-REQUEST.md)
for the request text and status tracking.

## What goes here

When the entitlements land:

- `CompradorUSBImagingDriver.iig` — interface declaration for the
  IOService subclass.
- `CompradorUSBImagingDriver.cpp` — IOUSBHostInterface match handler:
  `Start()`, `Stop()`, `NewUserClient()`.
- `CompradorUSBImagingClient.cpp` — IOUserClient subclass with the
  method table from
  [../docs/DEXT-DESIGN.md](../docs/DEXT-DESIGN.md#iouserclient-method-table).
- `Info.plist` — `IOKitPersonalities` matching class 6 / subclass 1
  with `IOProbeScore=90000`.
- `USBDriver.entitlements` — must contain
  `com.apple.developer.driverkit.transport.usb` keyed to the
  device classes Apple approves us for.

## What does NOT go here

- The host-app activation code (`DextActivator.swift`) lives in
  `MenuBarApp/Sources/`, not here.
- The Unix-socket IPC server (`SocketServer.swift`) lives in
  `MenuBarApp/Sources/`, not here.
- The patched libmtp lives at `bridge/cvendor/libmtp-comprador/`,
  not here.

## Build target

The dext is built only by Xcode (`xcodebuild -scheme USBDriver`); the
existing `make app-swiftc` path will not (and should not) build it.
See [../docs/DEXT-DESIGN.md](../docs/DEXT-DESIGN.md#build-pipeline).

---

*Owned by Dexter persona. Mercer changes welcome via PR with
`dexter/` review.*
