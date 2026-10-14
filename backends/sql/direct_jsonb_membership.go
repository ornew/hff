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
	needles := make([]hff.Node, n.ChildCount()-1)
	for i := range needles {
		needles[i], _ = n.Child(i + 1)
	}
	return b.directJSONBStringContainment(column, needles)
}

func (b *builder) directJSONBAnyEligible(n hff.Node) bool {
	_, ok := b.directJSONBStringPredicateNeedle(n, "any")
	return ok
}

// Binding identity, rather than the parameter's spelling, distinguishes the
// element tested by this callback from an outer or shadowed variable.
func (b *builder) directJSONBStringPredicateNeedle(n hff.Node, name string) (hff.Node, bool) {
	method, ok := n.Method()
	if !ok || method.Name() != name || n.ChildCount() != 2 {
		return hff.Node{}, false
	}
	receiver, _ := n.Child(0)
	if _, ok := b.directJSONBStringListColumn(receiver); !ok {
		return hff.Node{}, false
	}
	callback, _ := n.Child(1)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 1 {
		return hff.Node{}, false
	}
	body, _ := callback.Child(0)
	op, ok := body.Operator()
	if body.Kind() != hff.BinaryNode || !ok || op != hff.Equal {
		return hff.Node{}, false
	}
	for side := 0; side < 2; side++ {
		element, _ := body.Child(side)
		needle, _ := body.Child(1 - side)
		binding, parameter := element.Binding()
		value, literal := needle.Literal()
		if element.Kind() == hff.ParameterNode && parameter && binding.ID() == parameters[0].ID() &&
			literal && value.Kind() == hff.StringKind {
			return needle, true
		}
	}
	return hff.Node{}, false
}

func (b *builder) directJSONBAny(n hff.Node) (string, error) {
	receiver, _ := n.Child(0)
	column, _ := b.directJSONBStringListColumn(receiver)
	needle, _ := b.directJSONBStringPredicateNeedle(n, "any")
	return b.directJSONBStringContainment(column, []hff.Node{needle})
}

// A single literal equality has the same truth value for every String list
// element as JSONB contained-by. In particular, an empty list satisfies all,
// while a null element does not. Duplicates do not affect either result.
func (b *builder) directJSONBAllEligible(n hff.Node) bool {
	_, ok := b.directJSONBStringPredicateNeedle(n, "all")
	return ok
}

func (b *builder) directJSONBAll(n hff.Node) (string, error) {
	receiver, _ := n.Child(0)
	column, _ := b.directJSONBStringListColumn(receiver)
	needle, _ := b.directJSONBStringPredicateNeedle(n, "all")
	value, err := b.literal(needle)
	if err != nil {
		return "", err
	}
	return "(" + column + " IS NOT NULL AND " + column + " <@ jsonb_build_array(" + value.code + "))", nil
}

// JSONB object containment agrees with HFF equality for one known key whose
// value is String or Bool. It does not turn missing keys or JSON null into a
// match, and it never applies structural subset semantics to object equality.
func (b *builder) directJSONBObjectEqualParts(n hff.Node) (string, string, hff.Node, bool) {
	if n.Kind() != hff.BinaryNode {
		return "", "", hff.Node{}, false
	}
	op, ok := n.Operator()
	if !ok || op != hff.Equal {
		return "", "", hff.Node{}, false
	}
	left, _ := n.Child(0)
	right, _ := n.Child(1)
	if left.Kind() != hff.FieldNode || right.Kind() != hff.LiteralNode {
		return "", "", hff.Node{}, false
	}
	value, literal := right.Literal()
	if !literal || (value.Kind() != hff.StringKind && value.Kind() != hff.BoolKind) {
		return "", "", hff.Node{}, false
	}
	kind, scalar := scalarKind(left.Type())
	if !scalar || (kind != hff.StringType && kind != hff.BoolType) ||
		(kind == hff.StringType && value.Kind() != hff.StringKind) ||
		(kind == hff.BoolType && value.Kind() != hff.BoolKind) {
		return "", "", hff.Node{}, false
	}
	parent, _ := left.Child(0)
	if parent.Kind() != hff.VariableNode {
		return "", "", hff.Node{}, false
	}
	path, ok := parent.Path()
	if !ok {
		return "", "", hff.Node{}, false
	}
	key, _ := left.Field()
	for _, binding := range b.physicalBindings {
		if pathKey(binding.path) == pathKey(path) && binding.format == JSONB &&
			binding.metadata == nil && binding.structure == nil && binding.relation == nil && !binding.composite {
			return b.bindings[pathKey(path)], key, right, true
		}
	}
	return "", "", hff.Node{}, false
}

func (b *builder) directJSONBObjectEqualEligible(n hff.Node) bool {
	_, _, _, ok := b.directJSONBObjectEqualParts(n)
	return ok
}

func (b *builder) directJSONBObjectEqual(n hff.Node) (string, error) {
	column, key, right, _ := b.directJSONBObjectEqualParts(n)
	name, err := b.parameter(n, hff.String(key))
	if err != nil {
		return "", err
	}
	value, err := b.literal(right)
	if err != nil {
		return "", err
	}
	return "(" + column + " IS NOT NULL AND " + column + " @> jsonb_build_object(" +
		name.code + "," + value.code + "))", nil
}

func (b *builder) directJSONBStringContainment(column string, needles []hff.Node) (string, error) {
	arguments := make([]string, len(needles))
	for i, argument := range needles {
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
