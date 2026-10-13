package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Direct scalar tests need no intermediate relation. Nullable leaves include
// an explicit non-null test so the complete expression has the same Boolean
// result as HFF even outside a WHERE clause. Keep every other expression on
// the general lowering path.
func (b *builder) directScalarWhereEligible(n hff.Node) bool {
	switch n.Kind() {
	case hff.NotNode:
		child, _ := n.Child(0)
		return b.directScalarWhereEligible(child)
	case hff.AndNode, hff.OrNode:
		if n.ChildCount() == 0 {
			return false
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			if !b.directScalarWhereEligible(child) {
				return false
			}
		}
		return true
	case hff.BinaryNode:
		op, _ := n.Operator()
		switch op {
		case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
		default:
			return false
		}
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		if right.Kind() != hff.LiteralNode {
			return false
		}
		_, kind, ok := b.directScalarColumn(left)
		if !ok || (kind != hff.IntType && kind != hff.StringType && kind != hff.BoolType) {
			return false
		}
		value, _ := right.Literal()
		return (kind == hff.IntType && value.Kind() == hff.IntKind) ||
			(kind == hff.StringType && value.Kind() == hff.StringKind) ||
			(kind == hff.BoolType && value.Kind() == hff.BoolKind)
	case hff.NullTestNode:
		child, _ := n.Child(0)
		_, _, ok := b.directScalarColumn(child)
		return ok
	case hff.BoolNode:
		child, _ := n.Child(0)
		_, _, ok := b.directScalarColumn(child)
		return ok
	default:
		return false
	}
}

func (b *builder) directScalarColumn(n hff.Node) (string, hff.TypeKind, bool) {
	if n.Kind() != hff.VariableNode && n.Kind() != hff.FieldNode {
		return "", hff.InvalidType, false
	}
	for parent := n; parent.Kind() == hff.FieldNode; {
		parent, _ = parent.Child(0)
		if parent.Kind() != hff.VariableNode && parent.Kind() != hff.FieldNode {
			return "", hff.InvalidType, false
		}
	}
	path, ok := n.Path()
	if !ok {
		return "", hff.InvalidType, false
	}
	key := pathKey(path)
	if _, structured := b.jsonBindings[key]; structured {
		return "", hff.InvalidType, false
	}
	column := b.bindings[key]
	if column == "" {
		return "", hff.InvalidType, false
	}
	kind, ok := scalarKind(n.Type())
	if !ok || (kind != hff.IntType && kind != hff.StringType && kind != hff.BoolType) {
		return "", hff.InvalidType, false
	}
	return column, kind, true
}

func (b *builder) directScalarWhere(n hff.Node) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if n.Kind() == hff.NotNode {
		child, _ := n.Child(0)
		code, err := b.directScalarWhere(child)
		if err != nil {
			return "", err
		}
		return "(NOT " + code + ")", nil
	}
	if n.Kind() == hff.AndNode || n.Kind() == hff.OrNode {
		parts := make([]string, n.ChildCount())
		for i := range parts {
			child, _ := n.Child(i)
			part, err := b.directScalarWhere(child)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		join := " AND "
		if n.Kind() == hff.OrNode {
			join = " OR "
		}
		return "(" + strings.Join(parts, join) + ")", nil
	}
	if n.Kind() == hff.NullTestNode {
		child, _ := n.Child(0)
		column, _, _ := b.directScalarColumn(child)
		op, _ := n.Operator()
		if !acceptsKind(child.Type(), hff.NullType) {
			if op == hff.NotEqual {
				return "TRUE", nil
			}
			return "FALSE", nil
		}
		if op == hff.NotEqual {
			return "(" + column + " IS NOT NULL)", nil
		}
		return "(" + column + " IS NULL)", nil
	}
	if n.Kind() == hff.BoolNode {
		child, _ := n.Child(0)
		column, kind, _ := b.directScalarColumn(child)
		var test string
		switch kind {
		case hff.IntType:
			test = column + " <> 0"
		case hff.StringType:
			test = column + " COLLATE \"C\" <> '' COLLATE \"C\""
		default:
			test = column
		}
		if acceptsKind(child.Type(), hff.NullType) {
			test = column + " IS NOT NULL AND " + test
		}
		return "(" + test + ")", nil
	}
	left, _ := n.Child(0)
	right, _ := n.Child(1)
	column, kind, _ := b.directScalarColumn(left)
	value, err := b.literal(right)
	if err != nil {
		return "", err
	}
	if kind == hff.StringType {
		column += " COLLATE \"C\""
		value.code += " COLLATE \"C\""
	}
	op, _ := n.Operator()
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	test := column + " " + operator + " " + value.code
	if acceptsKind(left.Type(), hff.NullType) {
		// FALSE AND NULL is FALSE in PostgreSQL, so this stays a two-valued
		// Boolean without hiding the direct comparison from the planner.
		test = column + " IS NOT NULL AND " + test
	}
	return "(" + test + ")", nil
}
