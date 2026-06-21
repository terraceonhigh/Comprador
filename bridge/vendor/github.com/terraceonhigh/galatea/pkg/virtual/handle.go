package virtual

import "io"

// HandleResolver resolves a file handle — read from r in the same wire form a
// Leaf/Directory emits via the FileHandle attribute — back to the node it
// identifies. The NFSv4 server holds one of these to turn a PUTFH handle into
// a node. Lifted from bb-rex's virtual.HandleResolver (its handle-allocator
// machinery is otherwise not needed: Galatea backends mint their own handles).
type HandleResolver func(r io.ByteReader) (DirectoryChild, Status)
