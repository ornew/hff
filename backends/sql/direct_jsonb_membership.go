package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Only physical String lists use JSONB containment. Structural containment is
// subset comparison, unlike HFF equality, and tagged or numeric values need
// the general type-aware lowering.
func (b *builder) directJSONBStringListColumn(n hff.Node) (string, bool) {
	root := n
	for root.Kind() == hff.FieldNode {
		root, _ = root.Child(0)
	}
	if root.Kind() != hff.VariableNode || !strictStringList(n.Type()) {
		return "", false
	}
	path, ok := n.Path()
	if !ok {
		return "", false
	}
	key := pathKey(path)
	for _, binding := range b.physicalBindings {
		if pathKey(binding.path) == key && binding.format == JSONB &&
			binding.metadata == nil && binding.structure == nil && binding.relation == nil && !binding.composite {
			return b.bindings[key], true
		}
	}
	return "", false
}

func strictStringList(typ hff.Type) bool {
	if typ.Kind() == hff.ListType {
		return typ.Element().Kind() == hff.StringType
	}
	if typ.Kind() != hff.UnionType {
		return false
	}
	list := false
	for _, member := range typ.Members() {
		if member.Kind() == hff.NullType {
			continue
		}
		if !strictStringList(member) {
			return false
		}
		list = true
	}
	return list
}

func (b *builder) directJSONBContainsEligible(n hff.Node) bool {
	method, ok := n.Method()
	if !ok || method.Name() != "contains" || n.ChildCount() < 2 {
		return false
	}
	receiver, _ := n.Child(0)
	if _, ok := b.directJSONBStringListColumn(receiver); !ok {
		return false
	}
	for i := 1; i < n.ChildCount(); i++ {
		argument, _ := n.Child(i)
		value, literal := argument.Literal()
		if !literal || value.Kind() != hff.StringKind {
			return false
		}
	}
	return true
}

func (b *builder) directJSONBContains(n hff.Node) (string, error) {
	receiver, _ := n.Child(0)
	column, _ := b.directJSONBStringListColumn(receiver)
	arguments := make([]string, n.ChildCount()-1)
	for i := range arguments {
		argument, _ := n.Child(i + 1)
		value, err := b.literal(argument)
		if err != nil {
			return "", err
		}
		arguments[i] = value.code
	}
	// The filter boundary maps both SQL NULL and JSON null to false. Keeping
	// containment as a direct conjunct also exposes the column to GIN indexes.
	return "(" + column + " IS NOT NULL AND " + column + " @> jsonb_build_array(" + strings.Join(arguments, ",") + "))", nil
}
