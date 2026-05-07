<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Apple Entitlement Request — draft

Only one entitlement needs Apple review: the DriverKit USB Transport
entitlement for the dext. Filed at
https://developer.apple.com/contact/request/system-extension/.

**Correction (2026-05-04):** an earlier version of this doc described
a separate Request 1 for `com.apple.developer.system-extension.install`.
That entitlement does NOT require Apple review — it's a self-service
capability you toggle on the App ID at
https://developer.apple.com/account/resources/identifiers/list when
registering `com.comprador.app`. Tick the **System Extension**
capability there; no form to fill out.

---

## Pre-req: register both App IDs

Before filing the entitlement request, register both bundle IDs at
https://developer.apple.com/account/resources/identifiers/list :

| Bundle ID | Description | Capabilities to tick |
|-----------|-------------|----------------------|
| `com.comprador.app` | Comprador | System Extension |
| `com.comprador.app.USBDriver` | Comprador USB Driver | (none yet — DriverKit capability appears after the entitlement is granted) |

If you skip this and go straight to the entitlement form, Apple
returns "The provided bundle id is not valid or associated with your
account."

---

## The DriverKit USB Transport request

**Form fields (as actually presented by Apple's portal):**

| Field | Value |
|-------|-------|
| Company / Product URL | `https://github.com/terraceonhigh/Comprador` (confirm before pasting) |
| Which entitlement are you applying for? | `DriverKit Entitlement` |
| Which DriverKit entitlements do you need? | ☑ **USB Transport**, ☑ **UserClient Access** (no others) |
| USB Vendor ID | `6353` (decimal — Google's 0x18D1; see "the multi-vendor wrinkle" below) |
| UserClient Bundle IDs | `com.comprador.app.USBDriver` |

**The multi-vendor wrinkle:** Apple's form takes a single VID. Comprador
needs to access ~15 Android-phone vendors plus ~5 PTP-camera vendors,
none of which we manufacture. We seed with Google's VID (6353) because
Pixel is the obvious test target, and use the description to ask
Apple to scope by interface class rather than vendor ID. Expect
Apple's reply email to be where the real negotiation happens.

**Justification text (paste this into the description box):**

> The com.comprador.app.USBDriver DriverKit extension is part of
> Comprador, an open-source macOS utility that lets users mount
> Android phones and PTP/MTP cameras as Finder volumes over USB.
> Distribution is via Developer ID with notarization (outside the
> App Store).
>
> macOS's in-kernel USBImaging driver claims class 6 (USB Imaging
> Class) USB interfaces exclusively at enumeration. Comprador's
> userspace bridge therefore cannot access these interfaces if the
> user starts the app after plugging in a device. Userspace
> mitigations (USBDeviceOpenSeize, libusb_detach_kernel_driver,
> libusb_reset_device, launchctl bootout of ptpcamerad) all fail or
> require disabling SIP.
>
> The dext matches IOUSBHostInterface for bInterfaceClass=6 /
> bInterfaceSubClass=1 with a probe score that displaces USBImaging,
> performs bulk and interrupt USB transfers, and forwards them to
> the host app via IOUserClient. The host app routes those transfers
> to a bridge process that implements MTP/PTP over the now-available
> USB transport.
>
> Comprador is device-agnostic: it works against any class-6 USB
> Imaging interface, regardless of vendor. The relevant Android
> phone vendors are Google (0x18D1), Samsung (0x04E8), LG (0x1004),
> Motorola (0x22B8), Sony (0x0FCE), OnePlus (0x2A70), Xiaomi
> (0x2717), OPPO (0x22D9), Huawei (0x12D1), Asus (0x0B05), Lenovo
> (0x17EF), ZTE (0x19D2), Nokia (0x0421), Realme/BBK (0x1FC9),
> Nothing (0x2888). PTP camera vendors include Canon, Nikon, Sony,
> Fujifilm, and Panasonic.
>
> I am submitting this request with Google's vendor ID (6353
> decimal / 0x18D1) as the initial entry because Pixel devices are
> the most common test target. Please advise whether the entitlement
> can be scoped by interface class rather than vendor ID for this
> use case, or whether I should file separate per-vendor requests
> for the remaining manufacturers.
>
> I am not the manufacturer of any of the matched devices. The user
> physically plugs in the device they want to access; the dext
> performs no transfers on devices the user has not connected. No
> network traffic, no telemetry, no data persistence beyond what the
> user's file copy operation produces.

---

## Status tracking

Update this table as Apple responds.

| Step | Date | Notes |
|------|------|-------|
| App ID `com.comprador.app` registered | 2026-05-04 | System Extension capability ticked |
| App ID `com.comprador.app.USBDriver` registered | 2026-05-04 | DriverKit capability appears post-approval |
| DriverKit USB Transport request filed | 2026-05-04 | Seed VID 6353; expect Apple to email about multi-vendor scope |
| Apple first response | — | nudge by 2026-06-15 if still silent |
| DriverKit entitlement approved | — | — |

If Apple asks for clarification or scopes the approval narrowly,
paste the email verbatim under the table so future-Dexter has the
full thread.
