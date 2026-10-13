package sql

import "github.com/ornew/hff"

// Route dynamic comparisons to scalar or recursive-document lowering. A
// mismatched non-null runtime type yields ordinary null, not Boolean false;
// null operands and no-value operands consume their state as false instead.
func (b *builder) dynamicJSONComparison(n hff.Node, left, right expression, op hff.Operator) (expression, error) {
	if structuredKind(left.kind) || structuredKind(right.kind) {
		return b.dynamicJSONPair(n, left, right, op)
	}
	dynamic, scalar := left, right
	reversed := false
	if right.kind == hff.AnyType {
		if left.kind == hff.AnyType {
			return b.dynamicJSONPair(n, left, right, op)
		}
		dynamic, scalar, reversed = right, left, true
	}
	format, ok := b.jsonValues[dynamic.code]
	if !ok {
		return expression{}, b.failure(n, "dynamic comparison requires a recoverable JSON representation")
	}
	class := string(format) + "_typeof(" + dynamic.code + ")"
	text := "(" + dynamic.code + " #>> '{}'::text[])"
	nullOrAbsent := "(NOT (" + both(left.present, right.present) + ") OR " + left.code + " IS NULL OR " + right.code + " IS NULL)"
	compare := func(value expression) expression {
		if reversed {
			return b.scalarComparison(scalar, value, op)
		}
		return b.scalarComparison(value, scalar, op)
	}
	switch scalar.kind {
	case hff.StringType, hff.BoolType:
		if scalar.kind == hff.BoolType && op != hff.Equal && op != hff.NotEqual {
			return expression{}, b.failure(n, "Boolean values do not support order comparisons")
		}
		category := "string"
		if scalar.kind == hff.BoolType {
			category = "boolean"
		}
		matches := "(" + class + "='" + category + "')"
		if scalar.kind == hff.StringType {
			child := 0
			if reversed {
				child = 1
			}
			dynamicNode, _ := n.Child(child)
			if schemaMatch, handled := jsonUnionStringMatch(dynamic.code, format, b.jsonStoredType(dynamicNode), hff.PrimitiveType(hff.StringType)); handled {
				matches = schemaMatch
			}
		}
		value := expression{"(CASE WHEN " + matches + " THEN " + text + "::" + sqlType(scalar.kind) + " ELSE NULL END)", "TRUE", scalar.kind}
		comparison := compare(value)
		code := "(CASE WHEN " + nullOrAbsent + " THEN FALSE WHEN " + matches + " THEN " + comparison.code + " ELSE NULL::boolean END)"
		return expression{code, "TRUE", hff.BoolType}, nil
	case hff.DurationType:
		child := 0
		if reversed {
			child = 1
		}
		dynamicNode, _ := n.Child(child)
		if !jsonRootHasKind(b.jsonStoredType(dynamicNode), hff.DurationType) {
			return expression{}, b.failure(n, "dynamic JSON comparison cannot recover Duration identity")
		}
		matches := "(" + class + "='string')"
		value := expression{"(CASE WHEN " + matches + " THEN " + b.parseDurationInline(text) + " ELSE NULL::bigint END)", "TRUE", hff.DurationType}
		comparison := compare(value)
		code := "(CASE WHEN " + nullOrAbsent + " THEN FALSE WHEN " + matches + " THEN " + comparison.code + " ELSE NULL::boolean END)"
		return expression{code, "TRUE", hff.BoolType}, nil
	case hff.IntType, hff.FloatType:
		child := 0
		if reversed {
			child = 1
		}
		dynamicNode, _ := n.Child(child)
		canInt, canFloat := jsonNumericCandidates(dynamicNode.Type())
		if canInt && canFloat && format != JSON {
			return expression{}, b.failure(n, "dynamic JSONB numeric comparison requires type metadata")
		}
		matches := "(" + class + "='number')"
		integer, floating := "FALSE", "FALSE"
		if canInt {
			integer = matches
		}
		if canFloat {
			floating = matches
		}
		if canInt && canFloat {
			integer = both(matches, "("+text+" !~ '[.eE]')")
			floating = both(matches, "("+text+" ~ '[.eE]')")
		}
		intValue := expression{"(CASE WHEN " + integer + " THEN " + jsonScalarCast(text, hff.IntType) + " ELSE NULL END)", "TRUE", hff.IntType}
		floatValue := expression{"(CASE WHEN " + floating + " THEN " + jsonScalarCast(text, hff.FloatType) + " ELSE NULL END)", "TRUE", hff.FloatType}
		intComparison, floatComparison := compare(intValue), compare(floatValue)
		code := "(CASE WHEN " + nullOrAbsent + " THEN FALSE WHEN " + integer + " THEN " + intComparison.code + " WHEN " + floating + " THEN " + floatComparison.code + " ELSE NULL::boolean END)"
		return expression{code, "TRUE", hff.BoolType}, nil
	default:
		return expression{}, b.failure(n, "dynamic JSON comparison requires a supported scalar counterpart")
	}
}

// The inputs here are already typed SQL scalars. Keep mixed numeric comparison
// on its exact int64/float64 path rather than casting the integer to float64.
func (b *builder) scalarComparison(left, right expression, op hff.Operator) expression {
	if left.kind != right.kind {
		return b.mixedComparison(left, right, op)
	}
	leftCode, rightCode := left.code, right.code
	if left.kind == hff.StringType {
		leftCode += " COLLATE \"C\""
		rightCode += " COLLATE \"C\""
	}
	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	return boolean(both(both(left.present, right.present), "COALESCE(("+leftCode+" "+operator+" "+rightCode+"), FALSE)"))
}

// A schema can distinguish numeric subtypes even when their JSON category is
// shared. Only genuinely ambiguous numeric unions need preserved JSON spelling.
func jsonNumericCandidates(t hff.Type) (integer, floating bool) {
	switch t.Kind() {
	case hff.AnyType:
		return true, true
	case hff.IntType:
		return true, false
	case hff.FloatType:
		return false, true
	case hff.UnionType:
		for _, member := range t.Members() {
			i, f := jsonNumericCandidates(member)
			integer, floating = integer || i, floating || f
		}
	}
	return integer, floating
}

// These methods have only string receivers among JSON-native value kinds.
// Non-string runtime values become ordinary null, preserving partial method
// applicability rather than introducing a type guard's no-value state.
func (b *builder) dynamicJSONStringCall(n hff.Node, name string, values []expression) (expression, error) {
	converted := append([]expression(nil), values...)
	for i, value := range converted {
		if value.kind == hff.StringType || value.kind == hff.NullType {
			continue
		}
		format, ok := b.jsonValues[value.code]
		if !ok || value.kind != hff.AnyType {
			return expression{}, b.failure(n, "dynamic string methods require recoverable string candidates")
		}
		class := string(format) + "_typeof(" + value.code + ")"
		code := "(CASE WHEN " + class + "='string' THEN " + value.code + " #>> '{}'::text[] ELSE NULL::text END)"
		converted[i] = expression{code, value.present, hff.StringType}
	}
	return b.stringCall(n, name, converted)
}

func (b *builder) dynamicJSONBooleanCall(n hff.Node, name string, values []expression) (expression, error) {
	converted := append([]expression(nil), values...)
	for i, value := range converted {
		if value.kind == hff.BoolType || value.kind == hff.NullType {
			continue
		}
		format, ok := b.jsonValues[value.code]
		if !ok || value.kind != hff.AnyType {
			return expression{}, b.failure(n, "dynamic boolean methods require recoverable boolean candidates")
		}
		class := string(format) + "_typeof(" + value.code + ")"
		code := "(CASE WHEN " + class + "='boolean' THEN (" + value.code + " #>> '{}'::text[])::boolean ELSE NULL::boolean END)"
		converted[i] = expression{code, value.present, hff.BoolType}
	}
	return b.booleanCall(n, name, converted)
}
