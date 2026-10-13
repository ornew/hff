package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Numeric metadata is unnecessary when the opposite schema cannot contain
// numbers at any compared position. Keep unmatched JSONB numbers as data,
// rather than turning them into null and accidentally matching a null needle.
func (b *builder) comparisonDocumentAgainst(n hff.Node, raw string, format JSONFormat, typ, other hff.Type) (string, error) {
	if format == JSONB && jsonNativeSchema(typ) && !jsonMayContainNumbers(other) {
		return "COALESCE(" + raw + "::json,'null'::json)", nil
	}
	return b.comparisonDocument(n, raw, format, typ)
}

func jsonNativeSchema(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.NullType, hff.NeverType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType, hff.AnyType:
		return true
	case hff.ListType, hff.MapType:
		return jsonNativeSchema(typ.Element())
	case hff.UnionType:
		for _, member := range typ.Members() {
			if !jsonNativeSchema(member) {
				return false
			}
		}
		return true
	case hff.StructType:
		for _, field := range typ.Fields() {
			if !jsonNativeSchema(field.Type) {
				return false
			}
		}
		return true
	}
	return false
}

func jsonMayContainNumbers(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.IntType, hff.FloatType, hff.AnyType:
		return true
	case hff.ListType, hff.MapType:
		return jsonMayContainNumbers(typ.Element())
	case hff.UnionType:
		for _, member := range typ.Members() {
			if jsonMayContainNumbers(member) {
				return true
			}
		}
	case hff.StructType:
		for _, field := range typ.Fields() {
			if jsonMayContainNumbers(field.Type) {
				return true
			}
		}
	}
	return false
}

// comparisonDocument normalizes schema-known numeric subtypes before a dynamic
// tree walk. Any and ambiguous numbers require preserved JSON spelling; plain
// JSONB cannot recover those distinctions from value magnitude.
func (b *builder) comparisonDocument(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	if err := b.checkJSONStringSchemas(n, typ); err != nil {
		return "", err
	}
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if typ.Kind() == hff.AnyType {
		if format != JSON {
			return "", b.failure(n, "dynamic JSON equality requires preserved numeric types or type metadata")
		}
		return "COALESCE(" + raw + ",'null'::json)", nil
	}
	members := []hff.Type{typ}
	if typ.Kind() == hff.UnionType {
		members = typ.Members()
		for _, member := range members {
			if member.Kind() == hff.AnyType {
				return b.comparisonDocument(n, raw, format, member)
			}
		}
	}
	integer, floating := jsonNumericCandidates(typ)
	if integer && floating && format != JSON {
		return "", b.failure(n, "dynamic JSON equality requires preserved numeric types or type metadata")
	}
	class := string(format) + "_typeof(" + raw + ")"
	code := "CASE WHEN " + raw + " IS NULL OR " + class + "='null' THEN 'null'::json"
	// Merge structural alternatives just as field access does: each runtime
	// key/element is normalized using all applicable declared member types.
	var listTypes []hff.Type
	var objects []hff.Type
	for _, member := range members {
		condition := class + "='" + jsonCategory(member.Kind()) + "'"
		var value string
		switch member.Kind() {
		case hff.NullType, hff.NeverType:
			continue
		case hff.ListType:
			listTypes = append(listTypes, member.Element())
			continue
		case hff.StructType, hff.MapType:
			objects = append(objects, member)
			continue
		case hff.IntType, hff.FloatType:
			text := "(" + raw + " #>> '{}'::text[])"
			if integer && floating {
				operator := "!~"
				if member.Kind() == hff.FloatType {
					operator = "~"
				}
				condition = both(condition, text+" "+operator+" '[.eE]'")
			}
			value = jsonScalarCast(text, member.Kind()) + "::text"
			if member.Kind() == hff.FloatType {
				value = b.floatText(jsonScalarCast(text, member.Kind()))
			}
			value = "(" + value + ")::json"
		case hff.BytesType:
			value = "COALESCE(to_json(" + postgresBase64(jsonScalarCast("("+raw+" #>> '{}')", hff.BytesType)) + "),'null'::json)"
		case hff.DurationType, hff.TimestampType:
			value = raw + "::json"
		case hff.StringType, hff.BoolType:
			value = raw + "::json"
		default:
			return "", b.failure(n, "dynamic JSON comparison cannot recover this schema type")
		}
		code += " WHEN " + condition + " THEN " + value
	}
	if len(listTypes) > 0 {
		alias := b.nextAlias()
		item, err := b.comparisonDocument(n, alias+".value", format, hff.NewUnionType(listTypes...))
		if err != nil {
			return "", err
		}
		code += " WHEN " + class + "='array' THEN (SELECT COALESCE(json_agg(" + item + " ORDER BY " + alias + ".ord),'[]'::json) FROM " + string(format) + "_array_elements(" + raw + ") WITH ORDINALITY AS " + alias + "(value,ord))"
	}
	if len(objects) > 0 {
		alias := b.nextAlias()
		var fallback []hff.Type
		fields := map[string][]hff.Type{}
		var keys []string
		for _, object := range objects {
			if object.Kind() == hff.MapType {
				fallback = append(fallback, object.Element())
			} else {
				for _, field := range object.Fields() {
					if _, exists := fields[field.Name]; !exists {
						keys = append(keys, field.Name)
					}
					fields[field.Name] = append(fields[field.Name], field.Type)
				}
			}
		}
		item := "CASE"
		for _, key := range keys {
			parameter, err := b.parameter(n, hff.String(key))
			if err != nil {
				return "", err
			}
			choices := append(append([]hff.Type(nil), fields[key]...), fallback...)
			value, err := b.comparisonDocument(n, alias+".value", format, hff.NewUnionType(choices...))
			if err != nil {
				return "", err
			}
			item += " WHEN " + alias + ".key COLLATE \"C\"=" + parameter.code + " COLLATE \"C\" THEN " + value
		}
		otherwise, err := b.comparisonDocument(n, alias+".value", format, hff.NewUnionType(fallback...))
		if err != nil {
			return "", err
		}
		if len(keys) == 0 {
			item = otherwise
		} else {
			item += " ELSE " + otherwise + " END"
		}
		code += " WHEN " + class + "='object' THEN (SELECT COALESCE(json_object_agg(" + alias + ".key," + item + "),'{}'::json) FROM " + string(format) + "_each(" + raw + ") AS " + alias + "(key,value))"
	}
	return "(" + code + " ELSE 'null'::json END)", nil
}

func (b *builder) dynamicJSONPair(n hff.Node, left, right expression, op hff.Operator) (expression, error) {
	leftNode, _ := n.Child(0)
	rightNode, _ := n.Child(1)
	leftType, rightType := b.jsonStoredType(leftNode), b.jsonStoredType(rightNode)
	if jsonRecoverableTaggedScalarUnion(leftType) && jsonRecoverableTaggedScalarUnion(rightType) &&
		jsonRootHasKind(leftType, hff.TimestampType) == jsonRootHasKind(rightType, hff.TimestampType) {
		return b.dynamicJSONTaggedScalarPair(n, left, right, op, jsonRootHasKind(leftType, hff.TimestampType))
	}
	for i := 0; i < 2; i++ {
		child, _ := n.Child(i)
		if jsonHasTaggedString(b.jsonStoredType(child)) {
			return expression{}, b.failure(n, "dynamic JSON comparison cannot preserve Bytes, Duration, or Timestamp type identity")
		}
	}
	values := []expression{left, right}
	for i := range values {
		format, ok := b.jsonValues[values[i].code]
		if !ok {
			return expression{}, b.failure(n, "dynamic comparison requires recoverable JSON representations")
		}
		child, _ := n.Child(i)
		code, err := b.comparisonDocument(n, values[i].code, format, b.jsonStoredType(child))
		if err != nil {
			return expression{}, err
		}
		values[i].code = b.project(code)
	}
	left.code, right.code = values[0].code, values[1].code
	lc, rc := "json_typeof("+left.code+")", "json_typeof("+right.code+")"
	ln, rn := "("+left.code+" #>> '{}'::text[])", "("+right.code+" #>> '{}'::text[])"
	code := "CASE WHEN NOT (" + both(left.present, right.present) + ") OR " + left.code + " IS NULL OR " + right.code + " IS NULL OR " + lc + "='null' OR " + rc + "='null' THEN FALSE"
	for _, kind := range []hff.TypeKind{hff.StringType, hff.BoolType} {
		if kind == hff.BoolType && op != hff.Equal && op != hff.NotEqual {
			continue
		}
		comparison := b.scalarComparison(expression{jsonScalarCast(ln, kind), "TRUE", kind}, expression{jsonScalarCast(rn, kind), "TRUE", kind}, op)
		code += " WHEN " + both(lc+"='"+jsonCategory(kind)+"'", rc+"='"+jsonCategory(kind)+"'") + " THEN " + comparison.code
	}
	code += " WHEN " + both(lc+"='number'", rc+"='number'") + " THEN " + b.dynamicJSONNumbers(ln, rn, op)
	if op == hff.Equal || op == hff.NotEqual {
		equal := b.dynamicJSONTreeEqual(left.code, right.code)
		if op == hff.NotEqual {
			equal = "NOT " + equal
		}
		code += " WHEN " + both(lc+"="+rc, lc+" IN ('array','object')") + " THEN " + equal
	}
	return expression{"(" + code + " ELSE NULL::boolean END)", "TRUE", hff.BoolType}, nil
}

// Duration or Timestamp, Int, and Bool use different JSON categories. A declared union
// containing only these families can compare two dynamic values without
// external type metadata.
func (b *builder) dynamicJSONTaggedScalarPair(n hff.Node, left, right expression, op hff.Operator, timestamp bool) (expression, error) {
	lf, leftOK := b.jsonValues[left.code]
	rf, rightOK := b.jsonValues[right.code]
	if !leftOK || !rightOK {
		return expression{}, b.failure(n, "dynamic comparison requires recoverable JSON representations")
	}
	lc := string(lf) + "_typeof(" + left.code + ")"
	rc := string(rf) + "_typeof(" + right.code + ")"
	lt := "(" + left.code + " #>> '{}'::text[])"
	rt := "(" + right.code + " #>> '{}'::text[])"
	stringKind := hff.DurationType
	parse := b.parseDurationInline
	if timestamp {
		stringKind = hff.TimestampType
		parse = jsonTimestampNanoseconds
	}
	stringValue := b.scalarComparison(
		expression{parse(lt), "TRUE", stringKind},
		expression{parse(rt), "TRUE", stringKind}, op)
	integer := b.scalarComparison(
		expression{jsonScalarCast(lt, hff.IntType), "TRUE", hff.IntType},
		expression{jsonScalarCast(rt, hff.IntType), "TRUE", hff.IntType}, op)
	code := "(CASE WHEN NOT (" + both(left.present, right.present) + ") OR " + left.code + " IS NULL OR " + right.code + " IS NULL" +
		" THEN FALSE WHEN " + both(lc+"='string'", rc+"='string'") + " THEN " + stringValue.code +
		" WHEN " + both(lc+"='number'", rc+"='number'") + " THEN " + integer.code
	if op == hff.Equal || op == hff.NotEqual {
		boolean := b.scalarComparison(
			expression{jsonScalarCast(lt, hff.BoolType), "TRUE", hff.BoolType},
			expression{jsonScalarCast(rt, hff.BoolType), "TRUE", hff.BoolType}, op)
		code += " WHEN " + both(lc+"='boolean'", rc+"='boolean'") + " THEN " + boolean.code
	}
	code += " ELSE NULL::boolean END)"
	return expression{code, "TRUE", hff.BoolType}, nil
}

func (b *builder) dynamicJSONNumbers(left, right string, op hff.Operator) string {
	outer, paths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outer, paths }()
	var branches []string
	for _, l := range []hff.TypeKind{hff.IntType, hff.FloatType} {
		for _, r := range []hff.TypeKind{hff.IntType, hff.FloatType} {
			condition := func(text string, kind hff.TypeKind) string {
				operator := "!~"
				if kind == hff.FloatType {
					operator = "~"
				}
				return text + " " + operator + " '[.eE]'"
			}
			convert := func(text string, kind hff.TypeKind) expression {
				return expression{"(CASE WHEN " + condition(text, kind) + " THEN " + jsonScalarCast(text, kind) + " ELSE NULL END)", "TRUE", kind}
			}
			comparison := b.scalarComparison(convert(left, l), convert(right, r), op)
			branches = append(branches, "WHEN "+both(condition(left, l), condition(right, r))+" THEN "+comparison.code)
		}
	}
	code := "(CASE " + strings.Join(branches, " ") + " ELSE FALSE END)"
	if len(b.steps) > 0 {
		code = "(SELECT " + code + " FROM " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
	}
	return code
}

// Flattening both documents gives arbitrary-depth equality a finite SQL shape.
// Paths preserve list positions and actual object keysets, including null data.
func (b *builder) dynamicJSONTreeEqual(left, right string) string {
	walk := func(alias, source string) string {
		child := b.nextAlias()
		return alias + `(value,path) AS (SELECT ` + source + `,ARRAY[]::text[] UNION ALL
 SELECT ` + child + `.value,` + alias + `.path || ` + child + `.key FROM ` + alias + ` CROSS JOIN LATERAL (
 SELECT a.value,(a.ord-1)::text AS key FROM json_array_elements(CASE WHEN json_typeof(` + alias + `.value)='array' THEN ` + alias + `.value ELSE '[]'::json END) WITH ORDINALITY AS a(value,ord)
 UNION ALL SELECT o.value,o.key FROM json_each(CASE WHEN json_typeof(` + alias + `.value)='object' THEN ` + alias + `.value ELSE '{}'::json END) AS o(key,value)) AS ` + child + `)`
	}
	l, r := b.nextAlias(), b.nextAlias()
	lv, rv := l+".value", r+".value"
	lc, rc := "json_typeof("+lv+")", "json_typeof("+rv+")"
	lt, rt := "("+lv+" #>> '{}'::text[])", "("+rv+" #>> '{}'::text[])"
	equal := "CASE WHEN " + lv + " IS NULL OR " + rv + " IS NULL THEN FALSE WHEN " + lc + "<>" + rc + " THEN FALSE WHEN " + lc + " IN ('null','array','object') THEN TRUE WHEN " + lc + "='number' THEN " + b.dynamicJSONNumbers(lt, rt, hff.Equal) + " ELSE " + lt + " COLLATE \"C\"=" + rt + " COLLATE \"C\" END"
	return "(WITH RECURSIVE " + walk(l, left) + "," + walk(r, right) + " SELECT NOT EXISTS(SELECT 1 FROM " + l + " FULL JOIN " + r + " ON " + l + ".path COLLATE \"C\"=" + r + ".path COLLATE \"C\" WHERE " + l + ".path IS NULL OR " + r + ".path IS NULL OR NOT (" + equal + ")))"
}
