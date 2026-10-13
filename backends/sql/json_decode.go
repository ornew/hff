package sql

import "github.com/ornew/hff"

// jsonDecodeProjected decodes JSON already projected in the current SQL scope.
// Keeping the projection outside this helper permits reuse of validated parses.
func (b *builder) jsonDecodeProjected(n hff.Node, code string, format JSONFormat, typ hff.Type, present string) (expression, error) {
	kind, ok := jsonKind(typ)
	if !ok && jsonRootHasTaggedString(typ) && !jsonRecoverableTaggedScalarUnion(typ) {
		return expression{}, b.failure(n, "JSON decoding cannot preserve dynamic Bytes, Duration, or Timestamp type identity")
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
	case hff.TimestampType:
		text := "(" + normalized + " #>> '{}'::text[])"
		return expression{jsonTimestampNanoseconds(text), present, kind}, nil
	default:
		text := "(" + normalized + " #>> '{}'::text[])"
		return expression{jsonScalarCast(text, kind), present, kind}, nil
	}
}

func jsonRootHasTaggedString(typ hff.Type) bool {
	return jsonRootHasKind(typ, hff.BytesType) || jsonRootHasKind(typ, hff.DurationType) || jsonRootHasKind(typ, hff.TimestampType)
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

// One string-backed scalar, one numeric subtype, and Bool occupy disjoint JSON
// categories. A finite union of these types retains its runtime family without
// a type tag. Int|Float still needs preserved spelling or numeric metadata.
func jsonRecoverableTaggedScalarUnion(typ hff.Type) bool {
	if typ.Kind() != hff.UnionType {
		return false
	}
	tagged, other, integer, floating := false, false, false, false
	for _, member := range typ.Members() {
		switch member.Kind() {
		case hff.DurationType, hff.TimestampType:
			if tagged {
				return false
			}
			tagged = true
		case hff.IntType:
			integer, other = true, true
		case hff.FloatType:
			floating, other = true, true
		case hff.BoolType:
			other = true
		case hff.NullType:
		default:
			return false
		}
	}
	return tagged && other && !(integer && floating)
}
