package sql

import "github.com/ornew/hff"

// Schema non-nullability is trustworthy for a raw input column. It is not a
// proof for computed or decoded values, whose ordinary failures become null.
func (b *builder) nonnullScalarInput(n hff.Node, value expression) bool {
	if acceptsKind(n.Type(), hff.NullType) {
		return false
	}
	if b.qualifiedNativePath(n, value) {
		return true
	}
	switch n.Kind() {
	case hff.GuardNode, hff.TypeGuardNode:
		child, _ := n.Child(0)
		return b.nonnullScalarInput(child, value)
	case hff.VariableNode, hff.FieldNode, hff.ParameterNode:
		_, kind, native := b.directScalarColumn(n)
		return native && value.kind == kind
	}
	return false
}

// A raw JSON input container is governed by its declared schema before
// materialization. Computed values and metadata-restored inputs may already
// contain decoder-produced null children, so identical guards must inspect
// them instead of trusting the original static declaration.
func (b *builder) identicalInputGuard(n hff.Node) bool {
	kind, known := jsonKind(n.Type())
	if !known || (kind != hff.ListType && kind != hff.MapType && kind != hff.StructType) {
		return false
	}
	for n.Kind() == hff.GuardNode {
		n, _ = n.Child(0)
	}
	root := n
	for root.Kind() == hff.FieldNode {
		root, _ = root.Child(0)
	}
	if root.Kind() != hff.VariableNode {
		return false
	}
	path, ok := n.Path()
	if !ok {
		return false
	}
	for _, binding := range b.physicalBindings {
		if binding.format == "" || len(binding.path) > len(path) || pathKey(binding.path) != pathKey(path[:len(binding.path)]) {
			continue
		}
		return binding.metadata == nil
	}
	return false
}

// Match only the exact raw carrier registered by Build. JSON decoding,
// flattened parent state, Timestamp conversion, and callback parameters are
// not raw schema-column references, even if their static types are scalar.
func (b *builder) qualifiedNativePath(n hff.Node, value expression) bool {
	if n.Kind() != hff.VariableNode && n.Kind() != hff.FieldNode {
		return false
	}
	root := n
	for root.Kind() == hff.FieldNode {
		root, _ = root.Child(0)
	}
	if root.Kind() != hff.VariableNode || value.present != "TRUE" {
		return false
	}
	path, ok := n.Path()
	if !ok {
		return false
	}
	column := b.qualifiedNativePaths[pathKey(path)]
	return column != "" && value.code == column
}
