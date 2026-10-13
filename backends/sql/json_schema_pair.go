package sql

import (
	"strings"

	"github.com/ornew/hff"
)

type schemaJSONValue struct {
	code, category, text, null, floating string
	format                               JSONFormat
	stored, allowed                      comparisonShape
}

func (b *builder) schemaJSONValue(code string, format JSONFormat, stored, allowed hff.Type) schemaJSONValue {
	v := schemaJSONValue{code: code, format: format, stored: makeComparisonShape(stored), allowed: makeComparisonShape(allowed)}
	v.category = string(format) + "_typeof(" + code + ")"
	v.text = "(" + code + " #>> '{}'::text[])"
	v.null = "(" + code + " IS NULL OR " + v.category + "='null')"
	if v.stored.floating && acceptsKind(allowed, hff.FloatType) {
		family := v.category + "='number'"
		if v.stored.integer {
			family = both(family, v.text+" ~ '[.eE]'")
		}
		v.floating = b.project("(CASE WHEN " + family + " THEN " + jsonScalarCast(v.text, hff.FloatType) + " ELSE NULL::double precision END)")
		// The old document normalizer converted a rejected Float input to
		// JSON null. Keep that state for nested equality without serializing
		// the successfully decoded value or rebuilding its container.
		v.null = "(" + v.null + " OR (" + family + " AND " + v.floating + " IS NULL))"
	}
	return v
}

// This path consumes already-validated stored schemas. Each recursion keeps
// its projections in the element/key scope that owns the input expressions.
// Any remains a complete boundary to the existing generic equality path.
func (b *builder) schemaJSONEqual(n hff.Node, left string, lf JSONFormat, ls, lc hff.Type, right string, rf JSONFormat, rs, rc hff.Type, rootPresence string, op hff.Operator) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if makeComparisonShape(ls).any || makeComparisonShape(rs).any {
		return b.jsonEqual(n, left, lf, ls, right, rf, rs)
	}
	outer, paths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outer, paths }()
	l, r := b.schemaJSONValue(left, lf, ls, lc), b.schemaJSONValue(right, rf, rs, rc)
	code := "CASE WHEN " + l.null + " THEN " + r.null + " WHEN " + r.null + " THEN FALSE"
	if rootPresence != "" {
		code = "CASE WHEN NOT (" + rootPresence + ") OR " + l.null + " OR " + r.null + " THEN FALSE"
	}
	wrap := func(equal string) string {
		if rootPresence != "" && op == hff.NotEqual {
			return "NOT (" + equal + ")"
		}
		return equal
	}
	for _, kind := range []hff.TypeKind{hff.StringType, hff.BoolType} {
		if !acceptsKind(lc, kind) || !acceptsKind(rc, kind) {
			continue
		}
		comparison := b.scalarComparison(expression{jsonScalarCast(l.text, kind), "TRUE", kind}, expression{jsonScalarCast(r.text, kind), "TRUE", kind}, hff.Equal)
		code += " WHEN " + both(l.category+"='"+jsonCategory(kind)+"'", r.category+"='"+jsonCategory(kind)+"'") + " THEN " + wrap(comparison.code)
	}
	li, lfloat := jsonNumericCandidates(lc)
	ri, rfloat := jsonNumericCandidates(rc)
	lSide := jsonNumericSide{l.stored.integer && li, l.stored.floating && lfloat, l.stored.integer && l.stored.floating}
	rSide := jsonNumericSide{r.stored.integer && ri, r.stored.floating && rfloat, r.stored.integer && r.stored.floating}
	if (lSide.integer || lSide.floating) && (rSide.integer || rSide.floating) {
		equal := b.decodedJSONNumericPair(l.text, r.text, hff.Equal, lSide, rSide, l.floating, r.floating)
		code += " WHEN " + both(l.category+"='number'", r.category+"='number'") + " THEN " + wrap(equal)
	}
	if l.stored.list && r.stored.list && acceptsKind(lc, hff.ListType) && acceptsKind(rc, hff.ListType) {
		a, c := b.nextAlias(), b.nextAlias()
		equal, err := b.schemaJSONEqual(n, a+".value", lf, l.stored.element, schemaArrayElement(l.allowed), c+".value", rf, r.stored.element, schemaArrayElement(r.allowed), "", hff.Equal)
		if err != nil {
			return "", err
		}
		from := string(lf) + "_array_elements(" + l.container("array", "[]") + ") WITH ORDINALITY AS " + a + "(value,ord) FULL JOIN " + string(rf) + "_array_elements(" + r.container("array", "[]") + ") WITH ORDINALITY AS " + c + "(value,ord) ON " + a + ".ord=" + c + ".ord"
		equal = "(NOT EXISTS(SELECT 1 FROM " + from + " WHERE " + a + ".ord IS NULL OR " + c + ".ord IS NULL OR NOT " + equal + "))"
		code += " WHEN " + both(l.category+"='array'", r.category+"='array'") + " THEN " + wrap(equal)
	}
	object := func(t hff.Type) bool { return acceptsKind(t, hff.MapType) || acceptsKind(t, hff.StructType) }
	if l.stored.object && r.stored.object && object(lc) && object(rc) {
		equal, err := b.schemaJSONObjectEqual(n, l, r)
		if err != nil {
			return "", err
		}
		code += " WHEN " + both(l.category+"='object'", r.category+"='object'") + " THEN " + wrap(equal)
	}
	otherwise := "FALSE"
	if rootPresence != "" {
		otherwise = "NULL::boolean"
	}
	code = "(" + code + " ELSE " + otherwise + " END)"
	if len(b.steps) > 0 {
		code = "(SELECT " + code + " FROM " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
	}
	return code, nil
}

func schemaArrayElement(shape comparisonShape) hff.Type {
	if shape.any {
		return hff.PrimitiveType(hff.AnyType)
	}
	return shape.element
}

func schemaObjectValue(shape comparisonShape, key string) hff.Type {
	if shape.any {
		return hff.PrimitiveType(hff.AnyType)
	}
	if typ, ok := shape.fields[key]; ok {
		return typ
	}
	return shape.fallback
}

func schemaObjectFallback(shape comparisonShape) hff.Type {
	if shape.any {
		return hff.PrimitiveType(hff.AnyType)
	}
	return shape.fallback
}

func (v schemaJSONValue) container(category, empty string) string {
	return "(CASE WHEN " + v.category + "='" + category + "' THEN " + v.code + " ELSE '" + empty + "'::" + string(v.format) + " END)"
}

func (b *builder) schemaJSONObjectEqual(n hff.Node, l, r schemaJSONValue) (string, error) {
	a, c := b.nextAlias(), b.nextAlias()
	keys := append([]string(nil), l.stored.keys...)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		seen[key] = true
	}
	for _, key := range r.stored.keys {
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	value := "CASE"
	for _, key := range keys {
		ls, rs := schemaObjectValue(l.stored, key), schemaObjectValue(r.stored, key)
		if ls.Kind() == hff.NeverType || rs.Kind() == hff.NeverType {
			continue
		}
		parameter, err := b.parameter(n, hff.String(key))
		if err != nil {
			return "", err
		}
		equal, err := b.schemaJSONEqual(n, a+".value", l.format, ls, schemaObjectValue(l.allowed, key), c+".value", r.format, rs, schemaObjectValue(r.allowed, key), "", hff.Equal)
		if err != nil {
			return "", err
		}
		value += " WHEN " + a + ".key COLLATE \"C\"=" + parameter.code + " COLLATE \"C\" THEN " + equal
	}
	fallback := "FALSE"
	if l.stored.fallback.Kind() != hff.NeverType && r.stored.fallback.Kind() != hff.NeverType {
		var err error
		fallback, err = b.schemaJSONEqual(n, a+".value", l.format, l.stored.fallback, schemaObjectFallback(l.allowed), c+".value", r.format, r.stored.fallback, schemaObjectFallback(r.allowed), "", hff.Equal)
		if err != nil {
			return "", err
		}
	}
	if value == "CASE" {
		value = fallback
	} else {
		value = "(" + value + " ELSE " + fallback + " END)"
	}
	from := string(l.format) + "_each(" + l.container("object", "{}") + ") AS " + a + "(key,value) FULL JOIN " + string(r.format) + "_each(" + r.container("object", "{}") + ") AS " + c + "(key,value) ON " + a + ".key COLLATE \"C\"=" + c + ".key COLLATE \"C\""
	return "(NOT EXISTS(SELECT 1 FROM " + from + " WHERE " + a + ".key IS NULL OR " + c + ".key IS NULL OR NOT " + value + "))", nil
}
