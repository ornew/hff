package sql

import "github.com/ornew/hff"

// jsonDecodeProjected decodes JSON already projected in the current SQL scope.
// Keeping the projection outside this helper permits reuse of validated parses.
func (b *builder) jsonDecodeProjected(n hff.Node, code string, format JSONFormat, typ hff.Type, present string) (expression, error) {
	kind, ok := jsonKind(typ)
	if !ok && jsonRootHasTaggedString(typ) && !jsonRecoverableDurationScalarUnion(typ) {
		return expression{}, b.failure(n, "JSON decoding cannot preserve dynamic Bytes or Duration type identity")
	}
	if !ok {
		if typ.Kind() != hff.AnyType && typ.Kind() != hff.UnionType {
			return expression{}, b.failure(n, "JSON value requires a supported schema type")
		}
		if err := b.checkJSONStringSchemas(n, typ); err != nil {
			return expression{}, err
		}
		normalized := "(CASE WHEN " + string(format) + "_typeof(" + code + ") = 'null' THEN NULL ELSE " + code + " END)"
		b.jsonValues[normalized] = format
		return expression{normalized, present, hff.AnyType}, nil
	}
	normalized := "(CASE WHEN " + string(format) + "_typeof(" + code + ") = 'null' THEN NULL ELSE " + code + " END)"
	switch kind {
	case hff.NullType:
		return expression{"NULL", present, hff.NullType}, nil
	case hff.ListType, hff.MapType, hff.StructType:
		b.jsonValues[normalized] = format
		return expression{normalized, present, kind}, nil
	case hff.DurationType:
		text := "(" + normalized + " #>> '{}'::text[])"
		return expression{b.parseDurationInline(text), present, kind}, nil
	default:
		text := "(" + normalized + " #>> '{}'::text[])"
		return expression{jsonScalarCast(text, kind), present, kind}, nil
	}
}

func jsonRootHasTaggedString(typ hff.Type) bool {
	return jsonRootHasKind(typ, hff.BytesType) || jsonRootHasKind(typ, hff.DurationType)
}

func jsonRootHasKind(typ hff.Type, kind hff.TypeKind) bool {
	if typ.Kind() == kind {
		return true
	}
	if typ.Kind() == hff.UnionType {
		for _, member := range typ.Members() {
			if member.Kind() == kind {
				return true
			}
		}
	}
	return false
}

// Duration, Int, and Bool occupy disjoint JSON categories, so a finite union
// of these types can retain its runtime family without a custom type tag.
// Other tagged unions stay rejected until each observer can dispatch them.
func jsonRecoverableDurationScalarUnion(typ hff.Type) bool {
	if typ.Kind() != hff.UnionType {
		return false
	}
	duration, other := false, false
	for _, member := range typ.Members() {
		switch member.Kind() {
		case hff.DurationType:
			duration = true
		case hff.IntType, hff.BoolType:
			other = true
		case hff.NullType:
		default:
			return false
		}
	}
	return duration && other
}
