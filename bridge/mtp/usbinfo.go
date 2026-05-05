package mtp

/*
#cgo CFLAGS: -I/opt/homebrew/include/libusb-1.0
#cgo LDFLAGS: -lusb-1.0
#include <libusb.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"log"
	"unsafe"
)

// LogUSBInterfaces walks every USB device with the given vendor ID and
// dumps its full descriptor tree (configurations → interfaces → alt
// settings) to the log.
//
// We need this because libusb_claim_interface keeps failing with
// LIBUSB_ERROR_ACCESS even after we kill ptpcamerad. The most likely
// reason is that the phone is exposing a USB Still Image Class (class 6,
// PTP) interface, which the macOS kernel auto-binds its built-in driver
// to. No userspace daemon kill can free it. The MTP interface (class FF,
// vendor-specific) wouldn't have this problem.
//
// By logging the actual interface classes we can confirm whether (a) the
// device is exposing only a PTP interface in this state, (b) it's
// exposing both PTP and MTP and libmtp is opening the wrong one, or
// (c) something else entirely is going on.
func LogUSBInterfaces(vendorID uint16) {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		log.Printf("usbinfo: libusb_init failed: %d", rc)
		return
	}
	defer C.libusb_exit(ctx)

	var devices **C.libusb_device
	count := C.libusb_get_device_list(ctx, &devices)
	if count < 0 {
		log.Printf("usbinfo: libusb_get_device_list failed: %d", count)
		return
	}
	defer C.libusb_free_device_list(devices, 1)

	devSlice := unsafe.Slice(devices, int(count))
	matched := 0
	for _, dev := range devSlice {
		if dev == nil {
			continue
		}
		var desc C.struct_libusb_device_descriptor
		if rc := C.libusb_get_device_descriptor(dev, &desc); rc != 0 {
			continue
		}
		if uint16(desc.idVendor) != vendorID {
			continue
		}
		matched++

		log.Printf("usbinfo: device VID=0x%04x PID=0x%04x class=%d subclass=%d protocol=%d numConfigurations=%d",
			uint16(desc.idVendor), uint16(desc.idProduct),
			uint8(desc.bDeviceClass), uint8(desc.bDeviceSubClass),
			uint8(desc.bDeviceProtocol), uint8(desc.bNumConfigurations))

		for ci := 0; ci < int(desc.bNumConfigurations); ci++ {
			var cfg *C.struct_libusb_config_descriptor
			if rc := C.libusb_get_config_descriptor(dev, C.uint8_t(ci), &cfg); rc != 0 {
				log.Printf("usbinfo:   config[%d]: get_config_descriptor failed: %d", ci, rc)
				continue
			}

			numInterfaces := int(cfg.bNumInterfaces)
			log.Printf("usbinfo:   config[%d] value=%d numInterfaces=%d",
				ci, uint8(cfg.bConfigurationValue), numInterfaces)

			if numInterfaces > 0 && cfg._interface != nil {
				ifaces := unsafe.Slice(cfg._interface, numInterfaces)
				for ii, iface := range ifaces {
					numAlt := int(iface.num_altsetting)
					if numAlt == 0 || iface.altsetting == nil {
						continue
					}
					alts := unsafe.Slice(iface.altsetting, numAlt)
					for ai, alt := range alts {
						log.Printf("usbinfo:     interface[%d].alt[%d] class=%d (%s) subclass=%d protocol=%d numEndpoints=%d",
							ii, ai,
							uint8(alt.bInterfaceClass), classDescription(uint8(alt.bInterfaceClass)),
							uint8(alt.bInterfaceSubClass), uint8(alt.bInterfaceProtocol),
							uint8(alt.bNumEndpoints))
					}
				}
			}

			C.libusb_free_config_descriptor(cfg)
		}
	}
	log.Printf("usbinfo: enumeration complete (%d device(s) matching VID=0x%04x)", matched, vendorID)
}

// ResetDevice issues a USB-level port reset on the first matching device.
// This is the software equivalent of physically unplugging and replugging
// the cable — the kernel sees a detach + reattach and invalidates whatever
// state ptpcamerad (or the kernel-resident USBImaging driver) had cached
// against the device. Without this, killall-ing ptpcamerad doesn't actually
// release the interface: launchd respawns it within ~60ms and the new
// instance re-acquires the device before our claim window opens.
//
// Returns true if a reset was issued (bridge should exit so a fresh
// invocation can re-detect and claim the freshly-re-enumerated device).
func ResetDevice(vendorID uint16) bool {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		log.Printf("usbinfo: libusb_init failed for reset: %d", rc)
		return false
	}
	defer C.libusb_exit(ctx)

	var devices **C.libusb_device
	count := C.libusb_get_device_list(ctx, &devices)
	if count < 0 {
		log.Printf("usbinfo: libusb_get_device_list failed: %d", count)
		return false
	}
	defer C.libusb_free_device_list(devices, 1)

	devSlice := unsafe.Slice(devices, int(count))
	for _, dev := range devSlice {
		if dev == nil {
			continue
		}
		var desc C.struct_libusb_device_descriptor
		if C.libusb_get_device_descriptor(dev, &desc) != 0 {
			continue
		}
		if uint16(desc.idVendor) != vendorID {
			continue
		}

		var handle *C.libusb_device_handle
		if rc := C.libusb_open(dev, &handle); rc != 0 {
			log.Printf("usbinfo: libusb_open failed for VID=0x%04x PID=0x%04x: %d (continuing)",
				uint16(desc.idVendor), uint16(desc.idProduct), int(rc))
			continue
		}

		log.Printf("usbinfo: issuing USB port reset on VID=0x%04x PID=0x%04x (software unplug+replug)",
			uint16(desc.idVendor), uint16(desc.idProduct))
		rc := C.libusb_reset_device(handle)
		C.libusb_close(handle)

		switch rc {
		case 0:
			log.Printf("usbinfo: reset OK; device will re-enumerate, bridge exiting for fresh attempt")
			return true
		case C.LIBUSB_ERROR_NOT_FOUND:
			// macOS frequently returns NOT_FOUND because the reset
			// changed the device address mid-call — that's actually
			// the success signal we want.
			log.Printf("usbinfo: reset triggered re-enumeration (NOT_FOUND); bridge exiting for fresh attempt")
			return true
		default:
			log.Printf("usbinfo: libusb_reset_device returned %d", int(rc))
		}
	}
	return false
}

func classDescription(class uint8) string {
	switch class {
	case 0x00:
		return "device-defined"
	case 0x01:
		return "audio"
	case 0x02:
		return "communications"
	case 0x03:
		return "HID"
	case 0x05:
		return "physical"
	case 0x06:
		return "still-image/PTP — KERNEL CLAIMS THIS"
	case 0x07:
		return "printer"
	case 0x08:
		return "mass-storage"
	case 0x09:
		return "hub"
	case 0x0A:
		return "CDC-data"
	case 0x0B:
		return "smart-card"
	case 0x0E:
		return "video"
	case 0xE0:
		return "wireless"
	case 0xEF:
		return "miscellaneous"
	case 0xFE:
		return "application-specific"
	case 0xFF:
		return "vendor-specific (likely MTP)"
	default:
		return fmt.Sprintf("0x%02x", class)
	}
}
