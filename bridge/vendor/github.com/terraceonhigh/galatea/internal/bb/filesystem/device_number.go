package filesystem

// DeviceNumber stores a block or character device number, both as
// major/minor pair and the raw value. This is done because conversion
// between both formats is platform dependent and not always bijective.
//
// NOTE (Galatea vendor): bb-storage's originals (device_number_unix.go /
// device_number_nonunix.go) used golang.org/x/sys/unix for the platform
// dev_t packing. Galatea is macOS-only and keeps the module dependency-free
// (Milestone A, AC7), so the macOS/BSD dev_t encoding is inlined here:
//
//	makedev(maj, min) = (maj << 24) | min
//	major(raw)        = (raw >> 24) & 0xff
//	minor(raw)        = raw & 0xffffff
//
// (Apple's <sys/types.h>.) The exact raw encoding is immaterial to Galatea
// in practice — NFSv4 transmits device numbers as a major/minor specdata4
// pair, never the raw dev_t, and Galatea's backends serve no device nodes —
// but a self-consistent macOS-correct encoding is the honest choice. See
// internal/bb/VENDOR.md.
type DeviceNumber struct {
	major, minor uint32
	raw          uint64
}

// NewDeviceNumberFromMajorMinor creates a new device number based on a
// major/minor pair.
func NewDeviceNumberFromMajorMinor(major, minor uint32) DeviceNumber {
	return DeviceNumber{
		major: major,
		minor: minor,
		raw:   (uint64(major) << 24) | uint64(minor),
	}
}

// NewDeviceNumberFromRaw creates a new device number based on a raw
// value.
func NewDeviceNumberFromRaw(raw uint64) DeviceNumber {
	return DeviceNumber{
		major: uint32((raw >> 24) & 0xff),
		minor: uint32(raw & 0xffffff),
		raw:   raw,
	}
}

// ToMajorMinor returns the major/minor pair of the device number.
func (d DeviceNumber) ToMajorMinor() (uint32, uint32) {
	return d.major, d.minor
}

// ToRaw returns the raw value of the device number.
func (d DeviceNumber) ToRaw() uint64 {
	return d.raw
}
