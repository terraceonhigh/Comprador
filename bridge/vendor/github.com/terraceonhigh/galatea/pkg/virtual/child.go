package virtual

// Child is a variant type that contains either a directory or a leaf.
// Transcribed verbatim from bb-rex's virtual.Child (no dependencies).
//
// The 'kind' field exists because Go generics provide no nullable
// constraint, so a zero TDirectory/TLeaf cannot be compared against nil.
// See https://github.com/golang/go/issues/53656.
type Child[TDirectory any, TLeaf any, TNode any] struct {
	kind      int
	directory TDirectory
	leaf      TLeaf
}

// FromDirectory creates a Child that contains a directory.
func (Child[TDirectory, TLeaf, TNode]) FromDirectory(directory TDirectory) Child[TDirectory, TLeaf, TNode] {
	return Child[TDirectory, TLeaf, TNode]{kind: 1, directory: directory}
}

// FromLeaf creates a Child that contains a leaf.
func (Child[TDirectory, TLeaf, TNode]) FromLeaf(leaf TLeaf) Child[TDirectory, TLeaf, TNode] {
	return Child[TDirectory, TLeaf, TNode]{kind: 2, leaf: leaf}
}

// IsSet returns true if the Child contains either a directory or a leaf.
func (c Child[TDirectory, TLeaf, TNode]) IsSet() bool {
	return c.kind != 0
}

// GetNode returns the child as a single node, so methods common to both
// directories and leaves can be called.
func (c Child[TDirectory, TLeaf, TNode]) GetNode() TNode {
	switch c.kind {
	case 1:
		return any(c.directory).(TNode)
	case 2:
		return any(c.leaf).(TNode)
	default:
		panic("Child is not set")
	}
}

// GetPair returns the child as a (directory, leaf) pair, so type-specific
// methods can be called on whichever is set.
func (c Child[TDirectory, TLeaf, TNode]) GetPair() (TDirectory, TLeaf) {
	return c.directory, c.leaf
}
