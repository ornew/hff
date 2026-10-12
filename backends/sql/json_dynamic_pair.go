package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// comparisonDocument normalizes schema-known numeric subtypes before a dynamic
// tree walk. Any and ambiguous numbers require preserved JSON spelling; plain
// JSONB cannot recover those distinctions from value magnitude.
func (b *builder) comparisonDocument(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if typ.Kind() == hff.AnyType {
		if format != JSON {
			return "", b.failure(n, "dynamic JSON equality requires preserved numeric types or type metadata")
		}
		return raw, nil
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
	values := []expression{left, right}
	for i := range values {
		format, ok := b.jsonValues[values[i].code]
		if !ok {
			return expression{}, b.failure(n, "dynamic comparison requires recoverable JSON representations")
		}
		child, _ := n.Child(i)
		code, err := b.comparisonDocument(n, values[i].code, format, child.Type())
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
	equal := "CASE WHEN " + lc + "<>" + rc + " THEN FALSE WHEN " + lc + " IN ('null','array','object') THEN TRUE WHEN " + lc + "='number' THEN " + b.dynamicJSONNumbers(lt, rt, hff.Equal) + " ELSE " + lt + " COLLATE \"C\"=" + rt + " COLLATE \"C\" END"
	return "(WITH RECURSIVE " + walk(l, left) + "," + walk(r, right) + " SELECT NOT EXISTS(SELECT 1 FROM " + l + " FULL JOIN " + r + " ON " + l + ".path COLLATE \"C\"=" + r + ".path COLLATE \"C\" WHERE " + l + ".path IS NULL OR " + r + ".path IS NULL OR NOT (" + equal + ")))"
}
