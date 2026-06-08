package virtual

import "time"

// AttributesMask is a bitmask of status attributes that need to be
// requested through Node.VirtualGetAttributes(). Transcribed from
// bb-rex's virtual.AttributesMask.
type AttributesMask uint32

const (
	// AttributesMaskChangeID requests the change ID, which clients can
	// use to determine if file data, directory contents, or
	// attributes of the node have changed.
	AttributesMaskChangeID AttributesMask = 1 << iota
	// AttributesMaskDeviceNumber requests the raw device number
	// (st_rdev).
	AttributesMaskDeviceNumber
	// AttributesMaskFileHandle requests an identifier of the file that
	// is sufficient to resolve it at a later point in time.
	AttributesMaskFileHandle
	// AttributesMaskFileType requests the file type (upper 4 bits of
	// st_mode).
	AttributesMaskFileType
	// AttributesMaskHasNamedAttributes requests whether the file has
	// one or more named attributes.
	AttributesMaskHasNamedAttributes
	// AttributesMaskInodeNumber requests the inode number (st_ino).
	AttributesMaskInodeNumber
	// AttributesMaskIsInNamedAttributeDirectory requests whether the
	// node is inside a named attributes directory. Only regular files
	// and directories need provide it, as NFSv4 uses distinct file
	// types for those.
	AttributesMaskIsInNamedAttributeDirectory
	// AttributesMaskLastDataModificationTime requests the last data
	// modification time (st_mtim).
	AttributesMaskLastDataModificationTime
	// AttributesMaskLinkCount requests the link count (st_nlink).
	AttributesMaskLinkCount
	// AttributesMaskOwnerGroupID requests the ID of the group that
	// owns the file (st_gid).
	AttributesMaskOwnerGroupID
	// AttributesMaskOwnerUserID requests the ID of the user that owns
	// the file (st_uid).
	AttributesMaskOwnerUserID
	// AttributesMaskPermissions requests the permissions (lowest 12
	// bits of st_mode).
	AttributesMaskPermissions
	// AttributesMaskSizeBytes requests the file size (st_size). When
	// requested, this should be set for all file types except symbolic
	// links.
	AttributesMaskSizeBytes
	// AttributesMaskSymlinkTarget requests the target of a symbolic
	// link.
	AttributesMaskSymlinkTarget
	// AttributesMaskLastAccessTime requests the last access time
	// (st_atim). Added at the end of the block so existing bit values
	// are unchanged.
	AttributesMaskLastAccessTime
	// AttributesMaskSpaceTotal/Free/Avail request the filesystem's total,
	// free, and available-to-user space in BYTES (FATTR4_SPACE_TOTAL /
	// _FREE / _AVAIL) — what `df` and Finder show as volume capacity.
	AttributesMaskSpaceTotal
	AttributesMaskSpaceFree
	AttributesMaskSpaceAvail
	// AttributesMaskFilesTotal/Free/Avail request the filesystem's total,
	// free, and available file (inode) counts (FATTR4_FILES_TOTAL / _FREE /
	// _AVAIL) — `df`'s inode columns. All six are filesystem-wide; a backend
	// that can't report them simply leaves them unset (additive, non-breaking).
	AttributesMaskFilesTotal
	AttributesMaskFilesFree
	AttributesMaskFilesAvail
)

// Attributes of a file, normally requested through stat() or readdir().
// A bitmask tracks which attributes are set. Transcribed from bb-rex's
// virtual.Attributes. The symlink target is a path.Parser (via the Parser
// alias), matching bb-rex — DEC-005's string simplification is reverted in
// DEC-011 so the lifted server meets this interface without conversion.
type Attributes struct {
	fieldsPresent AttributesMask

	changeID                        uint64
	deviceNumber                    DeviceNumber
	fileHandle                      []byte
	fileType                        FileType
	hasNamedAttributes              bool
	inodeNumber                     uint64
	isInsideNamedAttributeDirectory bool
	lastAccessTime                  time.Time
	lastDataModificationTime        time.Time
	linkCount                       uint32
	ownerGroupID                    uint32
	ownerUserID                     uint32
	permissions                     Permissions
	sizeBytes                       uint64
	symlinkTarget                   Parser
	spaceTotal                      uint64
	spaceFree                       uint64
	spaceAvail                      uint64
	filesTotal                      uint64
	filesFree                       uint64
	filesAvail                      uint64
}

// GetFieldsPresent returns the mask of attributes that have been set.
// (Exported beyond bb-rex's package-private access, since Galatea's
// server lift may live in a sibling package.)
func (a *Attributes) GetFieldsPresent() AttributesMask {
	return a.fieldsPresent
}

// GetChangeID returns the change ID.
func (a *Attributes) GetChangeID() uint64 {
	if a.fieldsPresent&AttributesMaskChangeID == 0 {
		panic("The change ID attribute is mandatory, meaning it should be set when requested")
	}
	return a.changeID
}

// SetChangeID sets the change ID.
func (a *Attributes) SetChangeID(changeID uint64) *Attributes {
	a.changeID = changeID
	a.fieldsPresent |= AttributesMaskChangeID
	return a
}

// GetDeviceNumber returns the raw device number (st_rdev).
func (a *Attributes) GetDeviceNumber() (DeviceNumber, bool) {
	return a.deviceNumber, a.fieldsPresent&AttributesMaskDeviceNumber != 0
}

// SetDeviceNumber sets the raw device number (st_rdev).
func (a *Attributes) SetDeviceNumber(deviceNumber DeviceNumber) *Attributes {
	a.deviceNumber = deviceNumber
	a.fieldsPresent |= AttributesMaskDeviceNumber
	return a
}

// GetFileHandle returns an identifier sufficient to resolve the file
// later.
func (a *Attributes) GetFileHandle() []byte {
	if a.fieldsPresent&AttributesMaskFileHandle == 0 {
		panic("The file handle attribute is mandatory, meaning it should be set when requested")
	}
	return a.fileHandle
}

// SetFileHandle sets an identifier sufficient to resolve the file later.
func (a *Attributes) SetFileHandle(fileHandle []byte) *Attributes {
	a.fileHandle = fileHandle
	a.fieldsPresent |= AttributesMaskFileHandle
	return a
}

// GetFileType returns the file type (upper 4 bits of st_mode).
func (a *Attributes) GetFileType() FileType {
	if a.fieldsPresent&AttributesMaskFileType == 0 {
		panic("The file type attribute is mandatory, meaning it should be set when requested")
	}
	return a.fileType
}

// SetFileType sets the file type (upper 4 bits of st_mode).
func (a *Attributes) SetFileType(fileType FileType) *Attributes {
	a.fileType = fileType
	a.fieldsPresent |= AttributesMaskFileType
	return a
}

// GetHasNamedAttributes returns whether one or more named attributes are
// present.
func (a *Attributes) GetHasNamedAttributes() bool {
	if a.fieldsPresent&AttributesMaskHasNamedAttributes == 0 {
		panic("The \"has named attributes\" attribute is mandatory, meaning it should be set when requested")
	}
	return a.hasNamedAttributes
}

// SetHasNamedAttributes sets whether one or more named attributes are
// present.
func (a *Attributes) SetHasNamedAttributes(hasNamedAttributes bool) *Attributes {
	a.hasNamedAttributes = hasNamedAttributes
	a.fieldsPresent |= AttributesMaskHasNamedAttributes
	return a
}

// GetInodeNumber returns the inode number (st_ino).
func (a *Attributes) GetInodeNumber() uint64 {
	if a.fieldsPresent&AttributesMaskInodeNumber == 0 {
		panic("The inode number attribute is mandatory, meaning it should be set when requested")
	}
	return a.inodeNumber
}

// SetInodeNumber sets the inode number (st_ino).
func (a *Attributes) SetInodeNumber(inodeNumber uint64) *Attributes {
	a.inodeNumber = inodeNumber
	a.fieldsPresent |= AttributesMaskInodeNumber
	return a
}

// GetIsInNamedAttributeDirectory returns whether the node is inside a
// named attributes directory.
func (a *Attributes) GetIsInNamedAttributeDirectory() bool {
	if a.fieldsPresent&AttributesMaskIsInNamedAttributeDirectory == 0 {
		panic("The \"is in named attribute directory\" attribute is mandatory for regular files and directories, meaning it should be set when requested")
	}
	return a.isInsideNamedAttributeDirectory
}

// SetIsInNamedAttributeDirectory sets whether the node is inside a named
// attributes directory.
func (a *Attributes) SetIsInNamedAttributeDirectory(isInsideNamedAttributeDirectory bool) *Attributes {
	a.isInsideNamedAttributeDirectory = isInsideNamedAttributeDirectory
	a.fieldsPresent |= AttributesMaskIsInNamedAttributeDirectory
	return a
}

// GetLastAccessTime returns the last access time (st_atim).
func (a *Attributes) GetLastAccessTime() (time.Time, bool) {
	return a.lastAccessTime, a.fieldsPresent&AttributesMaskLastAccessTime != 0
}

// SetLastAccessTime sets the last access time (st_atim).
func (a *Attributes) SetLastAccessTime(lastAccessTime time.Time) *Attributes {
	a.lastAccessTime = lastAccessTime
	a.fieldsPresent |= AttributesMaskLastAccessTime
	return a
}

// --- filesystem-wide space/inode counts (statfs / df) ---

// GetSpaceTotal returns total filesystem space in bytes (FATTR4_SPACE_TOTAL).
func (a *Attributes) GetSpaceTotal() (uint64, bool) {
	return a.spaceTotal, a.fieldsPresent&AttributesMaskSpaceTotal != 0
}

// SetSpaceTotal sets total filesystem space in bytes.
func (a *Attributes) SetSpaceTotal(v uint64) *Attributes {
	a.spaceTotal = v
	a.fieldsPresent |= AttributesMaskSpaceTotal
	return a
}

// GetSpaceFree returns free filesystem space in bytes (FATTR4_SPACE_FREE).
func (a *Attributes) GetSpaceFree() (uint64, bool) {
	return a.spaceFree, a.fieldsPresent&AttributesMaskSpaceFree != 0
}

// SetSpaceFree sets free filesystem space in bytes.
func (a *Attributes) SetSpaceFree(v uint64) *Attributes {
	a.spaceFree = v
	a.fieldsPresent |= AttributesMaskSpaceFree
	return a
}

// GetSpaceAvail returns space available to the user in bytes (FATTR4_SPACE_AVAIL).
func (a *Attributes) GetSpaceAvail() (uint64, bool) {
	return a.spaceAvail, a.fieldsPresent&AttributesMaskSpaceAvail != 0
}

// SetSpaceAvail sets space available to the user in bytes.
func (a *Attributes) SetSpaceAvail(v uint64) *Attributes {
	a.spaceAvail = v
	a.fieldsPresent |= AttributesMaskSpaceAvail
	return a
}

// GetFilesTotal returns the total file (inode) count (FATTR4_FILES_TOTAL).
func (a *Attributes) GetFilesTotal() (uint64, bool) {
	return a.filesTotal, a.fieldsPresent&AttributesMaskFilesTotal != 0
}

// SetFilesTotal sets the total file (inode) count.
func (a *Attributes) SetFilesTotal(v uint64) *Attributes {
	a.filesTotal = v
	a.fieldsPresent |= AttributesMaskFilesTotal
	return a
}

// GetFilesFree returns the free file (inode) count (FATTR4_FILES_FREE).
func (a *Attributes) GetFilesFree() (uint64, bool) {
	return a.filesFree, a.fieldsPresent&AttributesMaskFilesFree != 0
}

// SetFilesFree sets the free file (inode) count.
func (a *Attributes) SetFilesFree(v uint64) *Attributes {
	a.filesFree = v
	a.fieldsPresent |= AttributesMaskFilesFree
	return a
}

// GetFilesAvail returns the file count available to the user (FATTR4_FILES_AVAIL).
func (a *Attributes) GetFilesAvail() (uint64, bool) {
	return a.filesAvail, a.fieldsPresent&AttributesMaskFilesAvail != 0
}

// SetFilesAvail sets the file count available to the user.
func (a *Attributes) SetFilesAvail(v uint64) *Attributes {
	a.filesAvail = v
	a.fieldsPresent |= AttributesMaskFilesAvail
	return a
}

// GetLastDataModificationTime returns the last data modification time
// (st_mtim).
func (a *Attributes) GetLastDataModificationTime() (time.Time, bool) {
	return a.lastDataModificationTime, a.fieldsPresent&AttributesMaskLastDataModificationTime != 0
}

// SetLastDataModificationTime sets the last data modification time
// (st_mtim).
func (a *Attributes) SetLastDataModificationTime(lastDataModificationTime time.Time) *Attributes {
	a.lastDataModificationTime = lastDataModificationTime
	a.fieldsPresent |= AttributesMaskLastDataModificationTime
	return a
}

// GetLinkCount returns the link count (st_nlink).
func (a *Attributes) GetLinkCount() uint32 {
	if a.fieldsPresent&AttributesMaskLinkCount == 0 {
		panic("The link count attribute is mandatory, meaning it should be set when requested")
	}
	return a.linkCount
}

// SetLinkCount sets the link count (st_nlink).
func (a *Attributes) SetLinkCount(linkCount uint32) *Attributes {
	a.linkCount = linkCount
	a.fieldsPresent |= AttributesMaskLinkCount
	return a
}

// GetOwnerGroupID returns the ID of the group owning the file (st_gid).
func (a *Attributes) GetOwnerGroupID() (uint32, bool) {
	return a.ownerGroupID, a.fieldsPresent&AttributesMaskOwnerGroupID != 0
}

// SetOwnerGroupID sets the ID of the group owning the file (st_gid).
func (a *Attributes) SetOwnerGroupID(ownerGroupID uint32) *Attributes {
	a.ownerGroupID = ownerGroupID
	a.fieldsPresent |= AttributesMaskOwnerGroupID
	return a
}

// GetOwnerUserID returns the ID of the user owning the file (st_uid).
func (a *Attributes) GetOwnerUserID() (uint32, bool) {
	return a.ownerUserID, a.fieldsPresent&AttributesMaskOwnerUserID != 0
}

// SetOwnerUserID sets the ID of the user owning the file (st_uid).
func (a *Attributes) SetOwnerUserID(ownerUserID uint32) *Attributes {
	a.ownerUserID = ownerUserID
	a.fieldsPresent |= AttributesMaskOwnerUserID
	return a
}

// GetPermissions returns the mode (lowest 12 bits of st_mode).
func (a *Attributes) GetPermissions() (Permissions, bool) {
	return a.permissions, a.fieldsPresent&AttributesMaskPermissions != 0
}

// SetPermissions sets the mode (lowest 12 bits of st_mode).
func (a *Attributes) SetPermissions(permissions Permissions) *Attributes {
	a.permissions = permissions
	a.fieldsPresent |= AttributesMaskPermissions
	return a
}

// GetSizeBytes returns the file size (st_size).
func (a *Attributes) GetSizeBytes() (uint64, bool) {
	return a.sizeBytes, a.fieldsPresent&AttributesMaskSizeBytes != 0
}

// SetSizeBytes sets the file size (st_size).
func (a *Attributes) SetSizeBytes(sizeBytes uint64) *Attributes {
	a.sizeBytes = sizeBytes
	a.fieldsPresent |= AttributesMaskSizeBytes
	return a
}

// GetSymlinkTarget returns the target of a symbolic link.
func (a *Attributes) GetSymlinkTarget() (Parser, bool) {
	return a.symlinkTarget, a.fieldsPresent&AttributesMaskSymlinkTarget != 0
}

// SetSymlinkTarget sets the target of a symbolic link.
func (a *Attributes) SetSymlinkTarget(symlinkTarget Parser) *Attributes {
	a.symlinkTarget = symlinkTarget
	a.fieldsPresent |= AttributesMaskSymlinkTarget
	return a
}
