package virtual

import "context"

// Node is the intersection between Directory and Leaf: the operations
// that apply to both kinds of object. Transcribed from bb-rex's
// virtual.Node. The five Apply* payload structs that accompanied it
// upstream were CAS/Bazel-specific and are dropped (see DEC-002); the
// VirtualApply extension hook remains, untyped, for hosts that want it.
type Node interface {
	VirtualGetAttributes(ctx context.Context, requested AttributesMask, attributes *Attributes)
	VirtualSetAttributes(ctx context.Context, in *Attributes, requested AttributesMask, attributes *Attributes) Status
	// VirtualApply sends data between backing nodes. It returns true
	// if the request was intercepted. The payload type is host-defined.
	VirtualApply(data any) bool
	// VirtualOpenNamedAttributes creates or looks up the named
	// attributes directory of a node.
	VirtualOpenNamedAttributes(ctx context.Context, createDirectory bool, requested AttributesMask, attributes *Attributes) (Directory, Status)
}

// GetFileInfo extracts a node's type and permissions and returns them as
// a FileInfo. Transcribed from bb-rex's virtual.GetFileInfo.
func GetFileInfo(name Component, node Node) FileInfo {
	var attributes Attributes
	node.VirtualGetAttributes(context.TODO(), AttributesMaskFileType|AttributesMaskPermissions, &attributes)
	permissions, ok := attributes.GetPermissions()
	if !ok {
		panic("Node did not return permissions attribute, even though it was requested")
	}
	return NewFileInfo(
		name,
		attributes.GetFileType(),
		permissions&PermissionsExecute != 0)
}
