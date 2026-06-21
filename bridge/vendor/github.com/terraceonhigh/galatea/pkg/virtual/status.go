package virtual

import "strconv"

// Status is the response of operations applied against Node objects.
// Transcribed verbatim from bb-rex's virtual.Status. Each value maps
// onto an NFSv4 error code in the server layer (lifted later).
type Status int

const (
	// StatusOK indicates that the operation succeeded.
	StatusOK Status = iota
	// StatusErrAccess indicates permission was denied.
	StatusErrAccess
	// StatusErrBadHandle indicates the file handle failed internal
	// consistency checks.
	StatusErrBadHandle
	// StatusErrExist indicates a file of the target name already
	// exists (when creating, renaming or linking).
	StatusErrExist
	// StatusErrInval indicates the arguments are not valid.
	StatusErrInval
	// StatusErrIO indicates an I/O error.
	StatusErrIO
	// StatusErrIsDir indicates a directory was given where the
	// operation does not allow one.
	StatusErrIsDir
	// StatusErrNoEnt indicates the target file does not exist.
	StatusErrNoEnt
	// StatusErrNotDir indicates a leaf was given where the operation
	// does not allow one.
	StatusErrNotDir
	// StatusErrNotEmpty indicates an attempt to remove a non-empty
	// directory.
	StatusErrNotEmpty
	// StatusErrNXIO indicates a request beyond the limits of the file
	// or device.
	StatusErrNXIO
	// StatusErrPerm indicates the caller is neither root nor the
	// owner of the target.
	StatusErrPerm
	// StatusErrROFS indicates a modifying operation on a read-only
	// file system.
	StatusErrROFS
	// StatusErrStale indicates the object the handle refers to no
	// longer exists, or access has been revoked.
	StatusErrStale
	// StatusErrSymlink indicates a symbolic link was given where the
	// operation does not allow one.
	StatusErrSymlink
	// StatusErrWrongType indicates the target is of an invalid type
	// for the operation and no more specific error applies.
	StatusErrWrongType
	// StatusErrXDev indicates an operation (such as linking) that
	// inappropriately crosses a boundary.
	StatusErrXDev
)

// String renders a Status as a short, stable name, for logging and CLI
// output. Unknown values render as "Status(N)".
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusErrAccess:
		return "Access"
	case StatusErrBadHandle:
		return "BadHandle"
	case StatusErrExist:
		return "Exist"
	case StatusErrInval:
		return "Inval"
	case StatusErrIO:
		return "IO"
	case StatusErrIsDir:
		return "IsDir"
	case StatusErrNoEnt:
		return "NoEnt"
	case StatusErrNotDir:
		return "NotDir"
	case StatusErrNotEmpty:
		return "NotEmpty"
	case StatusErrNXIO:
		return "NXIO"
	case StatusErrPerm:
		return "Perm"
	case StatusErrROFS:
		return "ROFS"
	case StatusErrStale:
		return "Stale"
	case StatusErrSymlink:
		return "Symlink"
	case StatusErrWrongType:
		return "WrongType"
	case StatusErrXDev:
		return "XDev"
	default:
		return "Status(" + strconv.Itoa(int(s)) + ")"
	}
}
