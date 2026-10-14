package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Callers distinguish a list's element pair from an argument that is itself
// a pair. A whole list of pairs must never be treated as one pair.
func (b *builder) jsonItemEquality(n hff.Node, left string, lf JSONFormat, lt hff.Type, l *jsonItemRoles, right string, rf JSONFormat, rt hff.Type, r *jsonItemRoles) (string, error) {
	if l != nil && r != nil {
		return b.jsonItemEqual(n, left, lf, l, right, rf, r)
	}
	return b.jsonEqual(n, left, lf, lt, right, rf, rt)
}

// Keys are String values at position zero. Value types are recovered at
// position one using the original object's per-key schema, not its merged
// homogeneous list union. Nested nulls remain equal; pair-null is data too.
func (b *builder) jsonItemEqual(n hff.Node, left string, lf JSONFormat, l *jsonItemRoles, right string, rf JSONFormat, r *jsonItemRoles) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	outer, paths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outer, paths }()
	ls, rs := makeComparisonShape(l.object), makeComparisonShape(r.object)
	keys := append([]string(nil), ls.keys...)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		seen[key] = true
	}
	for _, key := range rs.keys {
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	key := "(" + left + " ->> 0)"
	value := "CASE"
	for _, field := range keys {
		lt, rt := schemaObjectValue(ls, field), schemaObjectValue(rs, field)
		if lt.Kind() == hff.NeverType || rt.Kind() == hff.NeverType {
			continue
		}
		parameter, err := b.parameter(n, hff.String(field))
		if err != nil {
			return "", err
		}
		equal, err := b.jsonEqual(n, "("+left+" -> 1)", lf, lt, "("+right+" -> 1)", rf, rt)
		if err != nil {
			return "", err
		}
		value += " WHEN " + key + " COLLATE \"C\"=" + parameter.code + " COLLATE \"C\" THEN " + equal
	}
	fallback := "FALSE"
	lt, rt := schemaObjectFallback(ls), schemaObjectFallback(rs)
	if lt.Kind() != hff.NeverType && rt.Kind() != hff.NeverType {
		var err error
		fallback, err = b.jsonEqual(n, "("+left+" -> 1)", lf, lt, "("+right+" -> 1)", rf, rt)
		if err != nil {
			return "", err
		}
	}
	if value == "CASE" {
		value = fallback
	} else {
		value = "(" + value + " ELSE " + fallback + " END)"
	}
	lnull := "(" + left + " IS NULL OR " + string(lf) + "_typeof(" + left + ")='null')"
	rnull := "(" + right + " IS NULL OR " + string(rf) + "_typeof(" + right + ")='null')"
	code := "(CASE WHEN " + lnull + " THEN " + rnull + " WHEN " + rnull + " THEN FALSE WHEN " + key + " COLLATE \"C\" IS DISTINCT FROM (" + right + " ->> 0) COLLATE \"C\" THEN FALSE ELSE " + value + " END)"
	if len(b.steps) > 0 {
		code = "(SELECT " + code + " FROM " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
	}
	return code, nil
}

// Structural comparisons can retain whole item pairs or an ordered list of
// them. Element scopes own every projection used by their value comparisons.
func (b *builder) jsonItemComparison(n, leftNode, rightNode hff.Node, left, right expression, lf, rf JSONFormat) (string, bool, error) {
	if l, r := b.jsonItemRoles(leftNode), b.jsonItemRoles(rightNode); l != nil && r != nil {
		code, err := b.jsonItemEqual(n, left.code, lf, l, right.code, rf, r)
		return code, true, err
	}
	l, r := b.jsonItemsRoles(leftNode), b.jsonItemsRoles(rightNode)
	if l == nil || r == nil {
		return "", false, nil
	}
	a, c := b.nextAlias(), b.nextAlias()
	equal, err := b.jsonItemEqual(n, a+".value", lf, l, c+".value", rf, r)
	if err != nil {
		return "", true, err
	}
	from := string(lf) + "_array_elements(" + left.code + ") WITH ORDINALITY AS " + a + "(value,ord) FULL JOIN " + string(rf) + "_array_elements(" + right.code + ") WITH ORDINALITY AS " + c + "(value,ord) ON " + a + ".ord=" + c + ".ord"
	return "(NOT EXISTS(SELECT 1 FROM " + from + " WHERE " + a + ".ord IS NULL OR " + c + ".ord IS NULL OR NOT " + equal + "))", true, nil
}
