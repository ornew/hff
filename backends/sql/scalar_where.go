package sql

import (
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
)

// Direct filter tests need no intermediate relation. Nullable leaves include
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
		if b.directScalarLengthEligible(left, right) {
			return true
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
		if child.Kind() == hff.CallNode {
			return b.directScalarInEligible(child) || b.directScalarStringCallEligible(child) ||
				b.directJSONBContainsEligible(child) || b.directJSONBAnyEligible(child)
		}
		_, _, ok := b.directScalarColumn(child)
		return ok
	default:
		return false
	}
}

func (b *builder) directScalarLengthEligible(call, right hff.Node) bool {
	if call.Kind() != hff.CallNode || call.ChildCount() != 1 {
		return false
	}
	method, ok := call.Method()
	if !ok || method.Name() != "len" {
		return false
	}
	receiver, _ := call.Child(0)
	_, kind, ok := b.directScalarColumn(receiver)
	if !ok || kind != hff.StringType {
		return false
	}
	value, _ := right.Literal()
	return value.Kind() == hff.IntKind
}

func (b *builder) directScalarStringCallEligible(n hff.Node) bool {
	method, ok := n.Method()
	if !ok {
		return false
	}
	switch method.Name() {
	case "contains":
		if n.ChildCount() < 2 {
			return false
		}
	case "startswith", "endswith":
		if n.ChildCount() != 2 {
			return false
		}
	default:
		return false
	}
	receiver, _ := n.Child(0)
	_, kind, ok := b.directScalarColumn(receiver)
	if !ok || kind != hff.StringType {
		return false
	}
	for i := 1; i < n.ChildCount(); i++ {
		argument, _ := n.Child(i)
		if argument.Kind() != hff.LiteralNode {
			return false
		}
		value, _ := argument.Literal()
		if value.Kind() != hff.StringKind && value.Kind() != hff.NullKind {
			return false
		}
	}
	return true
}

func (b *builder) directScalarInEligible(n hff.Node) bool {
	method, ok := n.Method()
	if !ok || method.Name() != "in" || n.ChildCount() < 2 {
		return false
	}
	receiver, _ := n.Child(0)
	_, kind, ok := b.directScalarColumn(receiver)
	if !ok {
		return false
	}
	for i := 1; i < n.ChildCount(); i++ {
		argument, _ := n.Child(i)
		if argument.Kind() != hff.LiteralNode {
			return false
		}
		value, _ := argument.Literal()
		if value.Kind() == hff.NullKind {
			continue
		}
		if (kind != hff.IntType || value.Kind() != hff.IntKind) &&
			(kind != hff.StringType || value.Kind() != hff.StringKind) &&
			(kind != hff.BoolType || value.Kind() != hff.BoolKind) {
			return false
		}
	}
	return true
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
		if child.Kind() == hff.CallNode {
			if b.directScalarInEligible(child) {
				return b.directScalarIn(child)
			}
			if b.directJSONBContainsEligible(child) {
				return b.directJSONBContains(child)
			}
			if b.directJSONBAnyEligible(child) {
				return b.directJSONBAny(child)
			}
			return b.directScalarStringCall(child)
		}
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
	if b.directScalarLengthEligible(left, right) {
		return b.directScalarLengthComparison(n, left, right)
	}
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

func (b *builder) directScalarLengthComparison(n, call, right hff.Node) (string, error) {
	receiver, _ := call.Child(0)
	column, _, _ := b.directScalarColumn(receiver)
	value, err := b.literal(right)
	if err != nil {
		return "", err
	}
	op, _ := n.Operator()
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	test := "char_length(" + column + ")::bigint " + operator + " " + value.code
	if acceptsKind(receiver.Type(), hff.NullType) {
		test = column + " IS NOT NULL AND " + test
	}
	return "(" + test + ")", nil
}

// Literal string predicates need neither a method-result projection nor an
// HFF presence projection. A null receiver or argument is false in filter
// context, including inside a surrounding NOT.
func (b *builder) directScalarStringCall(n hff.Node) (string, error) {
	receiver, _ := n.Child(0)
	column, _, _ := b.directScalarColumn(receiver)
	method, _ := n.Method()
	name := method.Name()
	nullArgument := false
	for i := 1; i < n.ChildCount(); i++ {
		argument, _ := n.Child(i)
		value, _ := argument.Literal()
		if value.Kind() == hff.NullKind {
			nullArgument = true
			continue
		}
		str, _ := value.AsString()
		if !utf8.ValidString(str) || strings.ContainsRune(str, 0) {
			return "", b.failure(argument, "PostgreSQL text cannot encode invalid UTF-8 or NUL")
		}
	}
	if nullArgument {
		return "FALSE", nil
	}
	parts := make([]string, 0, n.ChildCount()-1)
	for i := 1; i < n.ChildCount(); i++ {
		argument, _ := n.Child(i)
		literal, err := b.literal(argument)
		if err != nil {
			return "", err
		}
		needle := literal.code
		switch name {
		case "contains":
			parts = append(parts, "(strpos("+column+" COLLATE \"C\", "+needle+" COLLATE \"C\") > 0)")
		case "startswith", "endswith":
			function := "left"
			if name == "endswith" {
				function = "right"
			}
			parts = append(parts, "("+function+"("+column+", char_length("+needle+")) COLLATE \"C\" = "+needle+" COLLATE \"C\")")
		}
	}
	test := strings.Join(parts, " AND ")
	if acceptsKind(receiver.Type(), hff.NullType) {
		test = column + " IS NOT NULL AND " + test
	}
	return "(" + test + ")", nil
}

func (b *builder) directScalarIn(n hff.Node) (string, error) {
	receiver, _ := n.Child(0)
	column, kind, _ := b.directScalarColumn(receiver)
	var needles []string
	for i := 1; i < n.ChildCount(); i++ {
		argument, _ := n.Child(i)
		value, _ := argument.Literal()
		if value.Kind() == hff.NullKind {
			continue
		}
		literal, err := b.literal(argument)
		if err != nil {
			return "", err
		}
		needle := literal.code
		if kind == hff.StringType {
			needle += " COLLATE \"C\""
		}
		needles = append(needles, needle)
	}
	if len(needles) == 0 {
		return "FALSE", nil
	}
	test := column
	if kind == hff.StringType {
		test += " COLLATE \"C\""
	}
	test += " IN (" + strings.Join(needles, ",") + ")"
	if acceptsKind(receiver.Type(), hff.NullType) {
		test = column + " IS NOT NULL AND " + test
	}
	return "(" + test + ")", nil
}
