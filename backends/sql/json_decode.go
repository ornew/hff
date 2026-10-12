package sql

import "github.com/ornew/hff"

// jsonDecodeProjected decodes JSON already projected in the current SQL scope.
// Keeping the projection outside this helper permits reuse of validated parses.
func (b *builder) jsonDecodeProjected(n hff.Node, code string, format JSONFormat, typ hff.Type, present string) (expression, error) {
	kind, ok := jsonKind(typ)
	if !ok {
		if typ.Kind() != hff.AnyType && typ.Kind() != hff.UnionType {
			return expression{}, b.failure(n, "JSON value requires a supported schema type")
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
	default:
		text := "(" + normalized + " #>> '{}'::text[])"
		return expression{jsonScalarCast(text, kind), present, kind}, nil
	}
}
