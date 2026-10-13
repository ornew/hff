package sql

import "github.com/ornew/hff"

// A definite container can use its existing length/keyset-aware recursion even
// when its children are Any. Overlapping structural unions retain the generic
// walk because one JSON category does not identify their declared member type.
func definiteJSONContainerPair(left, right hff.Type) bool {
	lkind, lok := jsonKind(left)
	rkind, rok := jsonKind(right)
	return lok && rok && structuredKind(lkind) && structuredKind(rkind) && jsonCategory(lkind) == jsonCategory(rkind)
}

// Specialize an unconstrained leaf against a known scalar. This is nested data
// equality: nulls compare equal and different runtime categories compare false.
// Top-level comparison presence/null rules remain in the enclosing expression.
func (b *builder) jsonAnyScalarEqual(n hff.Node, left string, lf JSONFormat, lt hff.Type, right string, rf JSONFormat, rt hff.Type) (string, bool, error) {
	if rt.Kind() == hff.AnyType && lt.Kind() != hff.AnyType {
		left, right, lf, rf, lt, rt = right, left, rf, lf, rt, lt
	}
	if lt.Kind() != hff.AnyType {
		return "", false, nil
	}
	if jsonHasTaggedString(rt) {
		return "", true, b.failure(n, "Any JSON equality cannot recover Bytes or Duration types")
	}
	kind, known := jsonKind(rt)
	if !known || structuredKind(kind) {
		return "", false, nil
	}
	// Keep the same representability checks as generic equality. In particular,
	// ambiguous JSONB numbers still need metadata when the counterpart is numeric.
	lcode, err := b.comparisonDocumentAgainst(n, left, lf, lt, rt)
	if err != nil {
		return "", true, err
	}
	rcode, err := b.comparisonDocumentAgainst(n, right, rf, rt, lt)
	if err != nil {
		return "", true, err
	}
	left, right = b.project(lcode), b.project(rcode)
	lclass, rclass := "json_typeof("+left+")", "json_typeof("+right+")"
	lnull := "(" + left + " IS NULL OR " + lclass + "='null')"
	rnull := "(" + right + " IS NULL OR " + rclass + "='null')"
	if kind == hff.NullType {
		return lnull, true, nil
	}
	ltext, rtext := "("+left+" #>> '{}'::text[])", "("+right+" #>> '{}'::text[])"
	category := jsonCategory(kind)
	lmatch, rmatch := lclass+"='"+category+"'", rclass+"='"+category+"'"
	value := func(text, condition string, typ hff.TypeKind) expression {
		return expression{"(CASE WHEN " + condition + " THEN " + jsonScalarCast(text, typ) + " ELSE NULL END)", "TRUE", typ}
	}
	scalar := value(rtext, rmatch, kind)
	var equal string
	if kind == hff.IntType || kind == hff.FloatType {
		integer := both(lmatch, ltext+" !~ '[.eE]'")
		floating := both(lmatch, ltext+" ~ '[.eE]'")
		intEqual := b.scalarComparison(value(ltext, integer, hff.IntType), scalar, hff.Equal)
		floatEqual := b.scalarComparison(value(ltext, floating, hff.FloatType), scalar, hff.Equal)
		equal = "(CASE WHEN " + integer + " THEN " + intEqual.code + " WHEN " + floating + " THEN " + floatEqual.code + " ELSE FALSE END)"
	} else {
		comparison := b.scalarComparison(value(ltext, lmatch, kind), scalar, hff.Equal)
		equal = "(CASE WHEN " + both(lmatch, rmatch) + " THEN " + comparison.code + " ELSE FALSE END)"
	}
	return "(CASE WHEN " + lnull + " THEN " + rnull + " WHEN " + rnull + " THEN FALSE ELSE " + equal + " END)", true, nil
}
