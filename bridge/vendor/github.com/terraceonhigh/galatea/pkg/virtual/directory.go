package virtual

import "context"

// DirectoryEntryReporter receives individual directory entries from
// VirtualReadDir(). Its methods may be called while locks on the
// underlying directory are held, so it is not safe to call methods of
// the child from within them. Transcribed from bb-rex.
type DirectoryEntryReporter interface {
	ReportEntry(nextCookie uint64, name Component, child DirectoryChild, attributes *Attributes) bool
}

// ChangeInfo is a pair of directory change IDs, before and after a
// mutating operation, as required by various NFSv4 operations.
// Transcribed from bb-rex.
type ChangeInfo struct {
	Before uint64
	After  uint64
}

// DirectoryChild is either a Directory or a Leaf, as returned by
// Directory.VirtualLookup().
type DirectoryChild = Child[Directory, Leaf, Node]

// Directory is a node exposed through NFSv4 (or FUSE). Every method is
// prefixed Virtual to avoid colliding with a host's own filesystem
// directory type. Transcribed from bb-rex's virtual.Directory, with
// path.Component replaced by Component, path.Parser (the symlink target)
// by a plain string, and filesystem.FileType by FileType.
type Directory interface {
	Node

	// VirtualOpenChild opens a regular file within the directory.
	//
	// When createAttributes is nil this fails with StatusErrNoEnt if
	// the file does not exist; when not nil, a file is created. When
	// existingOptions is nil this fails with StatusErrExist if the
	// file already exists; when not nil, an existing file is opened.
	// At least one of the two must be provided.
	VirtualOpenChild(ctx context.Context, name Component, shareAccess ShareMask, createAttributes *Attributes, existingOptions *OpenExistingOptions, requested AttributesMask, openedFileAttributes *Attributes) (Leaf, AttributesMask, ChangeInfo, Status)
	// VirtualLink links an existing file into the directory.
	VirtualLink(ctx context.Context, name Component, leaf Leaf, requested AttributesMask, attributes *Attributes) (ChangeInfo, Status)
	// VirtualLookup obtains the child stored under the given name.
	VirtualLookup(ctx context.Context, name Component, requested AttributesMask, out *Attributes) (DirectoryChild, Status)
	// VirtualMkdir creates an empty directory within this directory.
	VirtualMkdir(name Component, requested AttributesMask, attributes *Attributes) (Directory, ChangeInfo, Status)
	// VirtualMknod creates a character device, FIFO or UNIX domain
	// socket within this directory.
	VirtualMknod(ctx context.Context, name Component, fileType FileType, requested AttributesMask, attributes *Attributes) (Leaf, ChangeInfo, Status)
	// VirtualReadDir reports the files and directories stored within
	// this directory.
	VirtualReadDir(ctx context.Context, firstCookie uint64, requested AttributesMask, reporter DirectoryEntryReporter) Status
	// VirtualRename renames a file in this directory, potentially
	// moving it to another directory.
	VirtualRename(oldName Component, newDirectory Directory, newName Component) (ChangeInfo, ChangeInfo, Status)
	// VirtualRemove removes an empty directory or a leaf within this
	// directory. Depending on the flags it behaves like rmdir(),
	// unlink() or a mixture of the two (the latter is needed by
	// NFSv4).
	VirtualRemove(name Component, removeDirectory, removeLeaf bool) (ChangeInfo, Status)
	// VirtualSymlink creates a symbolic link within this directory.
	// pointedTo is the link's target path.
	VirtualSymlink(ctx context.Context, pointedTo Parser, linkName Component, requested AttributesMask, attributes *Attributes) (Leaf, ChangeInfo, Status)
}

const (
	// ImplicitDirectoryLinkCount is the link count to report for
	// directories whose contents are not defined explicitly (e.g.
	// lazy-loading or programmatically-infinite directories). A value
	// below two instructs tools like GNU find(1) to disable
	// optimizations that assume an accurate link count. Transcribed
	// from bb-rex.
	ImplicitDirectoryLinkCount uint32 = 1
	// EmptyDirectoryLinkCount is the link count to report for
	// directories that have no child directories.
	EmptyDirectoryLinkCount uint32 = 2
)
