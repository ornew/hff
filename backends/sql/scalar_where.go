package sql

import (
	"strings"

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
		case hff.Match, hff.NotMatch:
			return b.directScalarRegexEligible(n)
		case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
		default:
			return false
		}
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		if b.directScalarColumnComparisonEligible(n) {
			return true
		}
		value, constant, err := b.constantValue(right)
		if err != nil || !constant {
			return false
		}
		if b.directScalarLengthEligible(left, right) {
			return true
		}
		_, kind, ok := b.directScalarColumn(left)
		if !ok {
			return false
		}
		if _, mixed := mixedLiteralComparison(kind, op, value); mixed {
			return true
		}
		return (kind == hff.IntType && value.Kind() == hff.IntKind) ||
			(kind == hff.FloatType && value.Kind() == hff.FloatKind) ||
			(kind == hff.StringType && value.Kind() == hff.StringKind) ||
			(kind == hff.BoolType && value.Kind() == hff.BoolKind) ||
			(kind == hff.BytesType && value.Kind() == hff.BytesKind) ||
			(kind == hff.DurationType && value.Kind() == hff.DurationKind)
	case hff.NullTestNode:
		child, _ := n.Child(0)
		_, _, ok := b.directScalarColumn(child)
		return ok
	case hff.BoolNode:
		child, _ := n.Child(0)
		if b.directScalarRegexEligible(child) || b.directScalarExistsEligible(child) {
			return true
		}
		if child.Kind() == hff.CallNode {
			return b.directScalarInEligible(child) || b.directScalarStringCallEligible(child) ||
				b.directJSONBContainsEligible(child) || b.directJSONBAnyEligible(child)
		}
		_, _, ok := b.directScalarColumn(child)
		return ok
	default:
		return b.directScalarExistsEligible(n)
	}
}

func (b *builder) directScalarLengthEligible(call, right hff.Node) bool {
	if !builtinUnaryScalarMethod(call, "len") {
		return false
	}
	receiver, _ := call.Child(0)
	_, kind, ok := b.directScalarColumn(receiver)
	if !ok || (kind != hff.StringType && kind != hff.BytesType) {
		return false
	}
	value, constant, err := b.constantValue(right)
	return err == nil && constant && value.Kind() == hff.IntKind
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
		value, constant, err := b.constantValue(argument)
		if err != nil || !constant {
			return false
		}
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
		value, constant, err := b.constantValue(argument)
		if err != nil || !constant {
			return false
		}
		if value.Kind() == hff.NullKind {
			continue
		}
		if (kind != hff.IntType || value.Kind() != hff.IntKind) &&
			(kind != hff.FloatType || value.Kind() != hff.FloatKind) &&
			(kind != hff.StringType || value.Kind() != hff.StringKind) &&
			(kind != hff.BoolType || value.Kind() != hff.BoolKind) &&
			(kind != hff.BytesType || value.Kind() != hff.BytesKind) &&
			(kind != hff.DurationType || value.Kind() != hff.DurationKind) {
			return false
		}
	}
	return true
}

func (b *builder) directScalarColumn(n hff.Node) (string, hff.TypeKind, bool) {
	if column, kind, view := b.directScalarView(n); view {
		return column, kind, true
	}
	if identity, ok := b.identityPlan(n); ok {
		column, kind, direct := b.directScalarColumn(identity.operand)
		if !direct {
			return "", hff.InvalidType, false
		}
		if identity.kind == hff.FloatType && kind == hff.IntType {
			column = "(" + column + "::double precision)"
		}
		return column, identity.kind, true
	}
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
	if !ok || (kind != hff.IntType && kind != hff.FloatType && kind != hff.StringType && kind != hff.BoolType && kind != hff.BytesType && kind != hff.DurationType) {
		return "", hff.InvalidType, false
	}
	return column, kind, true
}

func (b *builder) directScalarWhere(n hff.Node) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if b.directScalarExistsEligible(n) {
		return b.directScalarExistsWhere(n), nil
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
		if b.directScalarExistsEligible(child) {
			return b.directScalarExistsWhere(child), nil
		}
		if b.directScalarRegexEligible(child) {
			return b.directScalarRegexWhere(child)
		}
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
		case hff.IntType, hff.FloatType, hff.DurationType:
			test = column + " <> 0"
		case hff.BytesType:
			test = "octet_length(" + column + ") > 0"
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
	if b.directScalarColumnComparisonEligible(n) {
		return b.directScalarColumnComparison(n), nil
	}
	if b.directScalarRegexEligible(n) {
		return b.directScalarRegexWhere(n)
	}
	if b.directScalarLengthEligible(left, right) {
		return b.directScalarLengthComparison(n, left, right)
	}
	column, kind, _ := b.directScalarColumn(left)
	op, _ := n.Operator()
	if literal, constant, err := b.constantValue(right); err != nil {
		return "", err
	} else if constant {
		if plan, mixed := mixedLiteralComparison(kind, op, literal); mixed {
			return b.directMixedLiteralComparison(left, right, column, plan)
		}
	}
	value, err := b.literal(right)
	if err != nil {
		return "", err
	}
	if kind == hff.StringType {
		column += " COLLATE \"C\""
		value.code += " COLLATE \"C\""
	}
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
	column, kind, _ := b.directScalarColumn(receiver)
	value, err := b.literal(right)
	if err != nil {
		return "", err
	}
	op, _ := n.Operator()
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	function := "char_length"
	if kind == hff.BytesType {
		function = "octet_length"
	}
	test := function + "(" + column + ")::bigint " + operator + " " + value.code
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
		value, _, err := b.constantValue(argument)
		if err != nil {
			return "", err
		}
		if value.Kind() == hff.NullKind {
			nullArgument = true
			continue
		}
		str, _ := value.AsString()
		if err := b.checkTextParameter(argument, str); err != nil {
			return "", err
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
		value, _, err := b.constantValue(argument)
		if err != nil {
			return "", err
		}
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
