package sql

import "github.com/ornew/hff"

func sharedJSONContainer(left, right hff.Type) bool {
	object := func(typ hff.Type) bool { return acceptsKind(typ, hff.MapType) || acceptsKind(typ, hff.StructType) }
	return (acceptsKind(left, hff.ListType) && acceptsKind(right, hff.ListType)) || (object(left) && object(right))
}

// Scalar branches consume the original, metadata-restored representation.
// Stored types determine numeric subtype identity, while compiled types limit
// reachable families. Canonical Float serialization is unnecessary for value
// comparison. The caller validates every stored alternative before this path.
func (b *builder) scalarJSONPair(n hff.Node, left expression, lf JSONFormat, lt hff.Type, right expression, rf JSONFormat, rt hff.Type, op hff.Operator) (expression, error) {
	leftNode, _ := n.Child(0)
	rightNode, _ := n.Child(1)
	lc, rc := string(lf)+"_typeof("+left.code+")", string(rf)+"_typeof("+right.code+")"
	ln, rn := "("+left.code+" #>> '{}'::text[])", "("+right.code+" #>> '{}'::text[])"
	code := "CASE WHEN NOT (" + both(left.present, right.present) + ") OR " + left.code + " IS NULL OR " + right.code + " IS NULL OR " + lc + "='null' OR " + rc + "='null' THEN FALSE"
	for _, kind := range []hff.TypeKind{hff.StringType, hff.BoolType} {
		if !acceptsKind(leftNode.Type(), kind) || !acceptsKind(rightNode.Type(), kind) || (kind == hff.BoolType && op != hff.Equal && op != hff.NotEqual) {
			continue
		}
		comparison := b.scalarComparison(expression{jsonScalarCast(ln, kind), "TRUE", kind}, expression{jsonScalarCast(rn, kind), "TRUE", kind}, op)
		code += " WHEN " + both(lc+"='"+jsonCategory(kind)+"'", rc+"='"+jsonCategory(kind)+"'") + " THEN " + comparison.code
	}
	leftInt, leftFloat := jsonNumericCandidates(lt)
	rightInt, rightFloat := jsonNumericCandidates(rt)
	leftAllowedInt, leftAllowedFloat := jsonNumericCandidates(leftNode.Type())
	rightAllowedInt, rightAllowedFloat := jsonNumericCandidates(rightNode.Type())
	l := jsonNumericSide{leftInt && leftAllowedInt, leftFloat && leftAllowedFloat, leftInt && leftFloat}
	r := jsonNumericSide{rightInt && rightAllowedInt, rightFloat && rightAllowedFloat, rightInt && rightFloat}
	if (l.integer || l.floating) && (r.integer || r.floating) {
		code += " WHEN " + both(lc+"='number'", rc+"='number'") + " THEN " + b.jsonNumericPair(ln, rn, op, l, r)
	}
	return expression{"(" + code + " ELSE NULL::boolean END)", "TRUE", hff.BoolType}, nil
}
