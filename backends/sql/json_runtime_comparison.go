package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Keep lexical value descriptors through comparison instead of merging tagged
// alternatives into an ordinary JSON schema. The probe owns all projections
// and parameters until it proves that at least one operand needs this path.
func (b *builder) deferredComparison(n hff.Node) (expression, bool, error) {
	if n.Kind() != hff.BinaryNode && n.Kind() != hff.NullTestNode {
		return expression{}, false, nil
	}
	op, _ := n.Operator()
	switch op {
	case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
	default:
		return expression{}, false, nil
	}
	if needed, err := b.hasDeferredValueSource(n); !needed || err != nil {
		return expression{}, err != nil, err
	}
	// Keep established native and recoverable-document comparisons when the
	// complete existing lowering succeeds. Typed carriers are a fallback for
	// values that cannot cross that ordinary materialization boundary.
	if n.Kind() == hff.BinaryNode {
		normal := b.validationBuilder()
		normal.keepDeferredValues = false
		value, err := normal.binary(n)
		if err == nil {
			b.adoptDeferredProbe(normal)
			return value, true, nil
		}
		if err := b.typedFallbackError(err); err != nil {
			return expression{}, true, err
		}
	}
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	leftNode, _ := n.Child(0)
	left, handled, err := probe.typedValueDescriptor(leftNode)
	if err != nil {
		return expression{}, true, err
	}
	if n.Kind() == hff.NullTestNode {
		if !handled {
			return expression{}, false, nil
		}
		code, err := probe.encodeRuntimeValue(leftNode, left)
		if err != nil {
			return expression{}, true, err
		}
		test := "(" + code + "->>0='null')"
		if op == hff.NotEqual {
			test = "(NOT " + test + ")"
		}
		b.adoptDeferredProbe(probe)
		return boolean(both(left.present, test)), true, nil
	}
	if !handled {
		parameter, err := probe.deferredValue(leftNode)
		if err != nil {
			return expression{}, true, err
		}
		left = deferredLeaf(parameter)
	}
	// Validate the left representation before visiting any right constructor.
	// If neither operand is deferred, discard this speculative encoding and
	// retain the ordinary comparison path and its representation requirements.
	lc, leftErr := probe.encodeRuntimeValue(leftNode, left)
	if leftErr != nil && handled {
		return expression{}, true, leftErr
	}
	if err := checkContext(probe.ctx); err != nil {
		return expression{}, true, err
	}
	rightNode, _ := n.Child(1)
	right, rightHandled, rightErr := probe.typedValueDescriptor(rightNode)
	if !handled && !rightHandled {
		return expression{}, rightErr != nil, rightErr
	}
	if leftErr != nil {
		return expression{}, true, leftErr
	}
	if rightErr != nil {
		return expression{}, true, rightErr
	}
	if !rightHandled {
		parameter, err := probe.deferredValue(rightNode)
		if err != nil {
			return expression{}, true, err
		}
		right = deferredLeaf(parameter)
	}
	rc, err := probe.encodeRuntimeValue(rightNode, right)
	if err != nil {
		return expression{}, true, err
	}
	lc, rc = probe.project(lc), probe.project(rc)
	result, err := probe.runtimeComparison(n, lc, rc, op, both(left.present, right.present))
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return result, true, err
}

// Avoid cloning builder state when there is no deferred value source. Row
// parameters, folds and explicit constructors can carry lexical typed values.
func (b *builder) hasDeferredValueSource(n hff.Node) (bool, error) {
	if _, needed, err := b.structValuesType(n); needed || err != nil {
		return needed, err
	}
	if _, needed, err := b.dynamicStructAccessType(n); needed || err != nil {
		return needed, err
	}
	if needed, err := b.deferredConstructorRoot(n); needed || err != nil {
		return needed, err
	}
	if binding, ok := n.Binding(); ok {
		p := b.parameters[binding.ID()]
		if p.deferred != nil || p.itemSlot != nil {
			return true, nil
		}
	}
	if builtinListCall(n, "foldl") || builtinListCall(n, "foldr") {
		return true, nil
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if needed, err := b.hasDeferredValueSource(child); needed || err != nil {
			return needed, err
		}
	}
	return false, nil
}

func (b *builder) typedValueDescriptor(n hff.Node) (*jsonDeferredValue, bool, error) {
	if value, handled, err := b.deferredDescriptor(n); handled || err != nil {
		return value, handled, err
	}
	if slot, handled, err := b.jsonItemSlot(n); handled || err != nil {
		return deferredLeaf(jsonParameter{typ: b.jsonStoredType(n), itemSlot: &slot, lossy: b.losesFloat(n, nil)}), handled, err
	}
	return nil, false, nil
}

func (b *builder) adoptDeferredProbe(probe builder) {
	memo, preserve := b.itemRoleMemo, b.keepDeferredValues
	*b = probe
	b.itemRoleMemo, b.keepDeferredValues = memo, preserve
}

func (b *builder) runtimeComparison(n hff.Node, left, right string, op hff.Operator, present string) (expression, error) {
	lt, rt := "("+left+"->>0)", "("+right+"->>0)"
	numbers := "(" + lt + " IN ('int','float') AND " + rt + " IN ('int','float'))"
	kinds := "'string','duration','timestamp'"
	if op == hff.Equal || op == hff.NotEqual {
		kinds += ",'bool','bytes','list','object'"
	}
	compatible := "(" + numbers + " OR (" + lt + "=" + rt + " AND " + lt + " IN (" + kinds + ")))"
	var comparison string
	var err error
	if op == hff.Equal || op == hff.NotEqual {
		comparison, err = b.runtimeTreeEqual(n, left, right)
		if op == hff.NotEqual {
			comparison = "(NOT " + comparison + ")"
		}
	} else {
		comparison, err = b.runtimeScalarCompare(n, left, right, op)
	}
	if err != nil {
		return expression{}, err
	}
	code := "(CASE WHEN NOT (" + present + ") OR " + lt + "='null' OR " + rt + "='null' THEN FALSE WHEN " + compatible + " THEN " + comparison + " ELSE NULL::boolean END)"
	return expression{code, "TRUE", hff.BoolType}, nil
}

// Decode each family behind its own tag discriminator. Local projections stay
// inside the scalar subquery, including when its inputs are recursive rows.
func (b *builder) runtimeScalarCompare(n hff.Node, left, right string, op hff.Operator) (string, error) {
	outer, paths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outer, paths }()
	decode := func(code string, kind hff.TypeKind) (expression, error) {
		raw := "(CASE WHEN " + code + "->>0='" + runtimeTag(kind) + "' THEN " + code + "->1 ELSE NULL::json END)"
		return b.jsonDecode(n, raw, JSON, hff.PrimitiveType(kind), "TRUE")
	}
	kinds := []hff.TypeKind{hff.IntType, hff.FloatType, hff.StringType, hff.DurationType, hff.TimestampType}
	if op == hff.Equal || op == hff.NotEqual {
		kinds = append(kinds, hff.BoolType, hff.BytesType)
	}
	leftValues, rightValues := map[hff.TypeKind]expression{}, map[hff.TypeKind]expression{}
	for _, kind := range kinds {
		for _, side := range []struct {
			code   string
			values map[hff.TypeKind]expression
		}{{left, leftValues}, {right, rightValues}} {
			value, err := decode(side.code, kind)
			if err != nil {
				return "", err
			}
			side.values[kind] = value
		}
	}
	var branches []string
	for _, lk := range kinds {
		for _, rk := range kinds {
			if lk != rk && !((lk == hff.IntType || lk == hff.FloatType) && (rk == hff.IntType || rk == hff.FloatType)) {
				continue
			}
			condition := "(" + left + "->>0='" + runtimeTag(lk) + "' AND " + right + "->>0='" + runtimeTag(rk) + "')"
			value := b.scalarComparison(leftValues[lk], rightValues[rk], op)
			branches = append(branches, "WHEN "+condition+" THEN "+value.code)
		}
	}
	code := "(CASE " + strings.Join(branches, " ") + " ELSE FALSE END)"
	if len(b.steps) > 0 {
		code = "(SELECT " + code + " FROM (VALUES (1)) AS " + b.nextAlias() + "(n) CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
	}
	return code, nil
}

// Flatten each typed tree once. Root tokens distinguish empty containers;
// child paths retain list positions and actual object keysets, so missing
// entries differ from stored nulls. Internal heterogeneous values are unequal.
func (b *builder) runtimeTreeEqual(n hff.Node, left, right string) (string, error) {
	walk := func(alias, source string) string {
		child := b.nextAlias()
		return alias + `(value,path) AS (SELECT ` + source + `,ARRAY[]::text[] UNION ALL
 SELECT ` + child + `.value,` + alias + `.path || ` + child + `.key FROM ` + alias + ` CROSS JOIN LATERAL (
 SELECT a.value,(a.ord-1)::text AS key FROM json_array_elements(CASE WHEN ` + alias + `.value->>0='list' THEN ` + alias + `.value->1 ELSE '[]'::json END) WITH ORDINALITY AS a(value,ord)
 UNION ALL SELECT o.value,o.key FROM json_each(CASE WHEN ` + alias + `.value->>0='object' THEN ` + alias + `.value->1 ELSE '{}'::json END) AS o(key,value)) AS ` + child + `)`
	}
	l, r := b.nextAlias(), b.nextAlias()
	lv, rv := l+".value", r+".value"
	lt, rt := "("+lv+"->>0)", "("+rv+"->>0)"
	scalar, err := b.runtimeScalarCompare(n, lv, rv, hff.Equal)
	if err != nil {
		return "", err
	}
	equal := "(CASE WHEN " + lt + "=" + rt + " AND " + lt + " IN ('null','list','object') THEN TRUE ELSE " + scalar + " END)"
	return "(WITH RECURSIVE " + walk(l, left) + "," + walk(r, right) + " SELECT NOT EXISTS(SELECT 1 FROM " + l + " FULL JOIN " + r + " ON " + l + ".path COLLATE \"C\"=" + r + ".path COLLATE \"C\" WHERE " + l + ".path IS NULL OR " + r + ".path IS NULL OR NOT (" + equal + ")))", nil
}
