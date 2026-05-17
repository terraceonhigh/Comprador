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
	"unsafe"
)

// LocationIDForBusAddr reconstructs the macOS IOKit USB Location ID for the
// device at the given libusb (bus_number, device_address) pair. The IOKit
// encoding macOS uses on the Swift side:
//
//	bits 24-31: USB bus number
//	bits 20-23: hub-1 port number (port on root hub)
//	bits 16-19: hub-2 port number
//	... (each subsequent nibble down the chain)
//	bits  0-3: deepest port number, or 0 if the device is plugged directly
//	           into a root hub
//
// libusb gives us the same information piecewise: bus number via
// libusb_get_bus_number, port chain via libusb_get_port_numbers. We
// reconstruct the IOKit encoding so the bridge's --device-loc-id flag can
// be matched against the value the Swift menu-bar app reads out of the
// IORegistry ("locationID" property on the USB IOService).
//
// Returns 0 and an error if no libusb device matches the (bus, addr) pair.
// Returns the computed location ID and nil on success.
//
// Note: on macOS, libmtp's LIBMTP_raw_device_t.bus_location field is the
// same as libusb's bus_number, and LIBMTP_raw_device_t.devnum is the same
// as libusb's device_address. So callers can extract these straight from
// the libmtp raw-device struct and feed them here.
func LocationIDForBusAddr(busNumber uint32, devAddress uint8) (uint32, error) {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		return 0, fmt.Errorf("libusb_init failed: %d", rc)
	}
	defer C.libusb_exit(ctx)

	var devices **C.libusb_device
	count := C.libusb_get_device_list(ctx, &devices)
	if count < 0 {
		return 0, fmt.Errorf("libusb_get_device_list failed: %d", count)
	}
	defer C.libusb_free_device_list(devices, 1)

	devSlice := unsafe.Slice(devices, int(count))
	for _, dev := range devSlice {
		if dev == nil {
			continue
		}
		bus := uint32(C.libusb_get_bus_number(dev))
		addr := uint8(C.libusb_get_device_address(dev))
		if bus != busNumber || addr != devAddress {
			continue
		}
		// Found our libusb device. Walk the port chain.
		var ports [7]C.uint8_t // 7 = USB spec max hub depth
		nports := C.libusb_get_port_numbers(dev, &ports[0], 7)
		if nports < 0 {
			return 0, fmt.Errorf("libusb_get_port_numbers failed: %d", nports)
		}
		locID := bus << 24
		// IOKit nibble layout puts root-hub-side port in the highest
		// non-bus nibble and walks downstream. libusb returns ports in
		// the same root-to-leaf order, so the loop maps directly.
		for i := 0; i < int(nports) && i < 6; i++ {
			locID |= uint32(ports[i]) << uint32(20-i*4)
		}
		return locID, nil
	}
	return 0, fmt.Errorf("no libusb device matched bus=%d addr=%d", busNumber, devAddress)
}
