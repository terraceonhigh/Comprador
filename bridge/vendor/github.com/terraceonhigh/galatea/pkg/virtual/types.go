package virtual

// This file re-points Galatea's FSAL leaf types to the vendored bb-storage
// types (internal/bb/filesystem and .../path), replacing the hand-cut natives
// introduced in DEC-005. They are *aliases*, not redefinitions: virtual.Component
// IS path.Component, virtual.FileType IS filesystem.FileType, and so on. That
// matters because the NFSv4 server lifted from bb-rex (R2d) speaks the vendored
// types directly — so with the interface here aliased to the same types, the
// server satisfies it with zero type conversion at the boundary. See DEC-011
// (the type fork resolved toward vendoring) and DEC-014 (the vendor itself).
//
// Re-exporting the leaf types and their constructors through this package also
// keeps the promise that a backend author's entire dependency surface is the
// `virtual` package: an FSAL implementation need not import internal/bb.

import (
	"github.com/terraceonhigh/galatea/internal/bb/filesystem"
	"github.com/terraceonhigh/galatea/internal/bb/filesystem/path"
)

// Leaf types, aliased to the vendored packages.
type (
	// Component is a pathname component: a string guaranteed to be a valid
	// filename.
	Component = path.Component
	// Parser parses a pathname string into a sequence of components. A
	// symbolic-link target takes this type.
	Parser = path.Parser
	// Format parses/stringifies pathnames in a particular OS convention.
	Format = path.Format
	// FileType enumerates the type of a file.
	FileType = filesystem.FileType
	// RegionType distinguishes data from holes for sparse-file seeking.
	RegionType = filesystem.RegionType
	// DeviceNumber identifies a device by major and minor number.
	DeviceNumber = filesystem.DeviceNumber
	// FileInfo is the name-and-type triple GetFileInfo returns.
	FileInfo = filesystem.FileInfo
)

// File type constants.
const (
	FileTypeRegularFile     = filesystem.FileTypeRegularFile
	FileTypeDirectory       = filesystem.FileTypeDirectory
	FileTypeSymlink         = filesystem.FileTypeSymlink
	FileTypeBlockDevice     = filesystem.FileTypeBlockDevice
	FileTypeCharacterDevice = filesystem.FileTypeCharacterDevice
	FileTypeFIFO            = filesystem.FileTypeFIFO
	FileTypeSocket          = filesystem.FileTypeSocket
	FileTypeOther           = filesystem.FileTypeOther

	// Data and Hole are the RegionType values used by VirtualSeek.
	Data = filesystem.Data
	Hole = filesystem.Hole
)

// Constructors, re-exported so callers keep using the virtual.* names (and so a
// backend need only import this package).
var (
	// NewComponent creates a pathname component, reporting whether the name
	// is valid (non-empty, not "." or "..", no slash or NUL).
	NewComponent = path.NewComponent
	// MustNewComponent is NewComponent that panics on an invalid name.
	MustNewComponent = path.MustNewComponent
	// NewFileInfo constructs a FileInfo.
	NewFileInfo = filesystem.NewFileInfo
	// UNIXFormat parses/stringifies UNIX-style ("/"-separated) pathnames. A
	// backend builds a symlink target with UNIXFormat.NewParser(target).
	UNIXFormat = path.UNIXFormat
)
