package virtual

// Permissions of a file. Unlike a regular UNIX file system, no
// distinction is made between owner, group and all permissions, because
// the virtual file system is effectively single user. Transcribed
// verbatim from bb-rex's virtual.Permissions (no bb-storage dependency).
type Permissions uint8

const (
	// PermissionsRead indicates that file contents may be read, or
	// that files in a directory may be listed.
	PermissionsRead Permissions = 1 << iota
	// PermissionsWrite indicates that file contents may be written
	// to, or that files in a directory may be added, removed or
	// renamed.
	PermissionsWrite
	// PermissionsExecute indicates that a file is executable, or that
	// files in a directory may be looked up.
	PermissionsExecute
)

// NewPermissionsFromMode creates a set of permissions from a traditional
// UNIX style mode.
func NewPermissionsFromMode(m uint32) (p Permissions) {
	if m&0o444 != 0 {
		p |= PermissionsRead
	}
	if m&0o222 != 0 {
		p |= PermissionsWrite
	}
	if m&0o111 != 0 {
		p |= PermissionsExecute
	}
	return p
}

// ToMode converts a set of permissions to a traditional UNIX style mode.
// The permissions for owner, group and all are identical.
func (p Permissions) ToMode() (m uint32) {
	if p&PermissionsRead != 0 {
		m |= 0o444
	}
	if p&PermissionsWrite != 0 {
		m |= 0o222
	}
	if p&PermissionsExecute != 0 {
		m |= 0o111
	}
	return m
}
