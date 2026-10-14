package sql

import "github.com/ornew/hff"

// Native scalar bindings have no separate presence flag. Structured, JSON,
// guarded, and timestamp paths do not pass directScalarColumn's carrier gate.
func (b *builder) directScalarExistsEligible(n hff.Node) bool {
	if !builtinUnaryScalarMethod(n, "exists") {
		return false
	}
	receiver, _ := n.Child(0)
	_, _, native := b.directScalarColumn(receiver)
	return native
}

func builtinUnaryScalarMethod(n hff.Node, name string) bool {
	if n.Kind() != hff.CallNode || n.ChildCount() != 1 {
		return false
	}
	method, ok := n.Method()
	if !ok || method.Name() != name {
		return false
	}
	for _, signature := range method.Signatures() {
		if _, custom := signature.Declaration(); custom {
			return false
		}
	}
	return true
}

// These builtins only expose an already native int64 carrier. They cannot
// fail on a valid non-null native operand, and SQL NULL propagates unchanged.
// Runtime parsing, Float rounding, and JSON decoding keep ordinary lowering.
func (b *builder) directScalarView(n hff.Node) (string, hff.TypeKind, bool) {
	method, ok := n.Method()
	if !ok || !builtinUnaryScalarMethod(n, method.Name()) {
		return "", hff.InvalidType, false
	}
	switch method.Name() {
	case "nanoseconds", "round", "ceil", "floor":
	default:
		return "", hff.InvalidType, false
	}
	receiver, _ := n.Child(0)
	column, kind, native := b.directScalarColumn(receiver)
	if !native {
		return "", hff.InvalidType, false
	}
	if (method.Name() == "nanoseconds" && kind == hff.DurationType) ||
		(method.Name() != "nanoseconds" && kind == hff.IntType) {
		return column, hff.IntType, true
	}
	return "", hff.InvalidType, false
}

func (b *builder) directScalarExistsWhere(n hff.Node) string {
	receiver, _ := n.Child(0)
	column, _, _ := b.directScalarColumn(receiver)
	if !acceptsKind(receiver.Type(), hff.NullType) {
		return "TRUE"
	}
	return "(" + column + " IS NOT NULL)"
}

func (b *builder) directScalarColumnComparisonEligible(n hff.Node) bool {
	if n.Kind() != hff.BinaryNode || n.ChildCount() != 2 {
		return false
	}
	op, _ := n.Operator()
	switch op {
	case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
	default:
		return false
	}
	left, _ := n.Child(0)
	right, _ := n.Child(1)
	_, lk, nativeLeft := b.directScalarColumn(left)
	_, rk, nativeRight := b.directScalarColumn(right)
	if !nativeLeft || !nativeRight || lk != rk {
		return false
	}
	// Bool and Bytes support equality only; mixed numeric families retain the
	// exact generic comparison instead of rounding Int into a Float carrier.
	return (lk != hff.BoolType && lk != hff.BytesType) || op == hff.Equal || op == hff.NotEqual
}

func (b *builder) directScalarColumnComparison(n hff.Node) string {
	left, _ := n.Child(0)
	right, _ := n.Child(1)
	lcode, kind, _ := b.directScalarColumn(left)
	rcode, _, _ := b.directScalarColumn(right)
	op, _ := n.Operator()
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	lcompare, rcompare := lcode, rcode
	if kind == hff.StringType {
		lcompare += " COLLATE \"C\""
		rcompare += " COLLATE \"C\""
	}
	test := lcompare + " " + operator + " " + rcompare
	if acceptsKind(right.Type(), hff.NullType) {
		test = rcode + " IS NOT NULL AND " + test
	}
	if acceptsKind(left.Type(), hff.NullType) {
		test = lcode + " IS NOT NULL AND " + test
	}
	return "(" + test + ")"
}
