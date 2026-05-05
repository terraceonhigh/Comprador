import Foundation
import IOKit
import IOKit.usb
import IOKit.usb.IOUSBLib

/// Forces macOS to release whatever kernel driver is bound to a USB device,
/// so the bridge's libusb_claim_interface can succeed on first attempt.
///
/// Background: when a phone enumerates with a USB Still Image Class
/// (class 6, PTP) interface, the macOS kernel auto-binds its built-in
/// USBImaging driver. libusb cannot detach that binding from userspace —
/// `libusb_detach_kernel_driver` returns "Invalid argument" and
/// `libusb_reset_device` returns LIBUSB_ERROR_NO_DEVICE because the call
/// path requires seized ownership. The only thing that actually works
/// without an admin password is going around libusb entirely and using
/// IOKit's USB Device family directly.
///
/// `IOUSBDeviceInterface::USBDeviceOpenSeize` requests exclusive access
/// to the device, which IOKit grants by terminating any other client of
/// that device. `USBDeviceReEnumerate(0)` then forces a USB-level
/// detach/reattach cycle — equivalent to a physical replug — after which
/// the kernel re-binds afresh but our seizing process keeps a brief
/// window of priority. We close immediately so the device is free for
/// the bridge process to libusb_claim.
///
/// Empirically this is the only path that works: kernel-driver detach
/// is forbidden, libusb reset fails on kernel-bound devices, and
/// killing ptpcamerad doesn't help because ptpcamerad isn't actually
/// the holder.
enum USBSeizer {

    enum Result {
        case success
        case deviceNotFound
        case pluginCreateFailed(IOReturn)
        case interfaceQueryFailed(Int32)
        case openSeizeFailed(IOReturn)
    }

    // Swift's clang importer can't translate the CFUUIDGetConstantUUIDWithBytes
    // macros that define these constants in IOKit headers, so we reconstruct
    // them from the byte values shown in the Apple-shipped IOUSBLib.h /
    // IOCFPlugIn.h. The byte sequences below match the headers verbatim.

    /// `9DC7B780-9EC0-11D4-A54F-000A27052861` — kIOUSBDeviceUserClientTypeID
    private static let deviceUserClientTypeUUID: CFUUID = CFUUIDCreateFromUUIDBytes(
        kCFAllocatorDefault,
        CFUUIDBytes(byte0: 0x9d, byte1: 0xc7, byte2: 0xb7, byte3: 0x80,
                    byte4: 0x9e, byte5: 0xc0, byte6: 0x11, byte7: 0xD4,
                    byte8: 0xa5, byte9: 0x4f, byte10: 0x00, byte11: 0x0a,
                    byte12: 0x27, byte13: 0x05, byte14: 0x28, byte15: 0x61)
    )

    /// `C244E858-109C-11D4-91D4-0050E4C6426F` — kIOCFPlugInInterfaceID
    private static let plugInInterfaceUUID: CFUUID = CFUUIDCreateFromUUIDBytes(
        kCFAllocatorDefault,
        CFUUIDBytes(byte0: 0xC2, byte1: 0x44, byte2: 0xE8, byte3: 0x58,
                    byte4: 0x10, byte5: 0x9C, byte6: 0x11, byte7: 0xD4,
                    byte8: 0x91, byte9: 0xD4, byte10: 0x00, byte11: 0x50,
                    byte12: 0xE4, byte13: 0xC6, byte14: 0x42, byte15: 0x6F)
    )

    /// `A33CF047-4B5B-48E2-B57D-0207FCEAE13B` — kIOUSBDeviceInterfaceID500
    private static let deviceInterface500UUID: CFUUID = CFUUIDCreateFromUUIDBytes(
        kCFAllocatorDefault,
        CFUUIDBytes(byte0: 0xA3, byte1: 0x3C, byte2: 0xF0, byte3: 0x47,
                    byte4: 0x4B, byte5: 0x5B, byte6: 0x48, byte7: 0xE2,
                    byte8: 0xB5, byte9: 0x7D, byte10: 0x02, byte11: 0x07,
                    byte12: 0xFC, byte13: 0xEA, byte14: 0xE1, byte15: 0x3B)
    )

    /// Seize the device, force a re-enumeration, release. Should be called
    /// from the main thread or any thread; IOKit calls are thread-safe.
    static func seizeAndReset(vendorID: UInt16, productID: UInt16) -> Result {
        // Build a matching dictionary for the specific device. Modern
        // macOS uses "IOUSBHostDevice" — same class DeviceWatcher.swift
        // watches, so we know the matching works.
        guard let matchingDict = IOServiceMatching("IOUSBHostDevice") as NSMutableDictionary? else {
            return .deviceNotFound
        }
        matchingDict[kUSBVendorID] = NSNumber(value: vendorID)
        matchingDict[kUSBProductID] = NSNumber(value: productID)

        let service = IOServiceGetMatchingService(kIOMainPortDefault, matchingDict)
        guard service != IO_OBJECT_NULL else {
            return .deviceNotFound
        }
        defer { IOObjectRelease(service) }

        // Create the IOCFPlugInInterface — the COM-style entry point for
        // talking to a USB device from userspace.
        var plugInPtr: UnsafeMutablePointer<UnsafeMutablePointer<IOCFPlugInInterface>?>?
        var score: Int32 = 0
        let pluginResult = IOCreatePlugInInterfaceForService(
            service,
            USBSeizer.deviceUserClientTypeUUID,
            USBSeizer.plugInInterfaceUUID,
            &plugInPtr,
            &score
        )
        guard pluginResult == kIOReturnSuccess,
              let plugIn = plugInPtr,
              let plugInInterface = plugIn.pointee?.pointee
        else {
            return .pluginCreateFailed(pluginResult)
        }
        defer {
            _ = plugIn.pointee?.pointee.Release(plugIn)
        }

        // QueryInterface for IOUSBDeviceInterface500.
        var rawInterface: UnsafeMutableRawPointer? = nil
        let queryResult = withUnsafeMutablePointer(to: &rawInterface) { ptr -> Int32 in
            plugInInterface.QueryInterface(
                plugIn,
                CFUUIDGetUUIDBytes(USBSeizer.deviceInterface500UUID),
                ptr
            )
        }
        guard queryResult == 0, let raw = rawInterface else {
            return .interfaceQueryFailed(queryResult)
        }
        let deviceInterface = raw.assumingMemoryBound(
            to: UnsafeMutablePointer<IOUSBDeviceInterface500>?.self
        )
        defer {
            _ = deviceInterface.pointee?.pointee.Release(deviceInterface)
        }

        guard let device = deviceInterface.pointee?.pointee else {
            return .interfaceQueryFailed(-1)
        }

        // The seize: tells IOKit "give me this device and evict whoever
        // currently has it." For class-6 (PTP) devices the eviction
        // includes unwinding ptpcamerad's open file descriptors; the
        // kernel driver itself stays bound but its userspace clients
        // are gone.
        let openResult = device.USBDeviceOpenSeize(deviceInterface)
        guard openResult == kIOReturnSuccess else {
            return .openSeizeFailed(openResult)
        }

        // Force a USB-level detach/reattach. This is the equivalent of
        // physical replug — the device drops off the bus and re-enumerates,
        // which fully invalidates the prior kernel driver instance. After
        // this call returns, the device is back online with a fresh
        // descriptor handle.
        _ = device.USBDeviceReEnumerate(deviceInterface, 0)

        // Release. The kernel will re-bind its driver to the
        // re-enumerated device, but there's a window before that happens
        // where libusb can claim cleanly. The bridge spawn that follows
        // hits that window.
        _ = device.USBDeviceClose(deviceInterface)

        return .success
    }
}
