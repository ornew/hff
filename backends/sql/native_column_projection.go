package sql

import "github.com/ornew/hff"

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
