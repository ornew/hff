package sql

import "github.com/ornew/hff"

// Direct regex lowering is limited to filter predicates over native String
// columns and actual literals. Value contexts retain nullable regex results.
func (b *builder) directScalarRegexEligible(n hff.Node) bool {
	if n.ChildCount() != 2 {
		return false
	}
	switch n.Kind() {
	case hff.BinaryNode:
		op, _ := n.Operator()
		if op != hff.Match && op != hff.NotMatch {
			return false
		}
	case hff.CallNode:
		method, ok := n.Method()
		if !ok || method.Name() != "matches" {
			return false
		}
		for _, signature := range method.Signatures() {
			if _, custom := signature.Declaration(); custom {
				return false
			}
		}
	default:
		return false
	}
	receiver, _ := n.Child(0)
	_, kind, native := b.directScalarColumn(receiver)
	if !native || kind != hff.StringType {
		return false
	}
	pattern, _ := n.Child(1)
	value, literal := pattern.Literal()
	return literal && (value.Kind() == hff.StringKind || value.Kind() == hff.NullKind)
}

func (b *builder) directScalarRegexWhere(n hff.Node) (string, error) {
	receiver, _ := n.Child(0)
	column, _, _ := b.directScalarColumn(receiver)
	pattern, _ := n.Child(1)
	translated, valid, err := b.literalRegexPattern(pattern)
	if err != nil {
		return "", err
	}
	if !valid {
		return "FALSE", nil
	}
	arg, err := b.parameter(pattern, hff.String(translated))
	if err != nil {
		return "", err
	}
	operator := "~"
	if op, _ := n.Operator(); op == hff.NotMatch {
		// !~ is false for null receivers; outer NOT of ~ remains true.
		operator = "!~"
	}
	// The native case-sensitive operator has the same default non-newline-
	// sensitive mode as regexp_like(...,'cs'). Keep the null mask outside
	// the operator so a compatible regex index can recognize its condition.
	return "(" + column + " IS NOT NULL AND " + column + " COLLATE \"C\" " + operator + " " + arg.code + ")", nil
}
