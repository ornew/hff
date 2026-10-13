package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Numeric metadata is unnecessary when the opposite schema cannot contain
// numbers at any compared position. Keep unmatched JSONB numbers as data,
// rather than turning them into null and accidentally matching a null needle.
func (b *builder) comparisonDocumentAgainst(n hff.Node, raw string, format JSONFormat, typ, other hff.Type) (string, error) {
	if keepRawJSONComparisonNumbers(format, typ, other) {
		return "COALESCE(" + raw + "::json,'null'::json)", nil
	}
	return b.comparisonDocument(n, raw, format, typ)
}

// A nonnumeric counterpart cannot match numeric data. Keeping the raw JSONB
// number also prevents a rejected Float decode from becoming a null match.
func keepRawJSONComparisonNumbers(format JSONFormat, typ, other hff.Type) bool {
	return format == JSONB && jsonNativeSchema(typ) && !jsonMayContainNumbers(other)
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
	if err := b.checkComparisonDocumentSchema(n, format, typ); err != nil {
		return "", err
	}
	return b.normalizedComparisonDocument(n, raw, format, typ)
}

func (b *builder) normalizedComparisonDocument(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	shape := makeComparisonShape(typ)
	if shape.any {
		return "COALESCE(" + raw + ",'null'::json)", nil
	}

	class := string(format) + "_typeof(" + raw + ")"
	code := "CASE WHEN " + raw + " IS NULL OR " + class + "='null' THEN 'null'::json"
	// Merge structural alternatives just as field access does: each runtime
	// key/element is normalized using all applicable declared member types.
	for _, member := range shape.members {
		condition := class + "='" + jsonCategory(member.Kind()) + "'"
		var value string
		switch member.Kind() {
		case hff.NullType, hff.NeverType:
			continue
		case hff.ListType, hff.StructType, hff.MapType:
			continue
		case hff.IntType, hff.FloatType:
			text := "(" + raw + " #>> '{}'::text[])"
			if shape.integer && shape.floating {
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
	if shape.list {
		alias := b.nextAlias()
		item, err := b.normalizedComparisonDocument(n, alias+".value", format, shape.element)
		if err != nil {
			return "", err
		}
		code += " WHEN " + class + "='array' THEN (SELECT COALESCE(json_agg(" + item + " ORDER BY " + alias + ".ord),'[]'::json) FROM " + string(format) + "_array_elements(" + raw + ") WITH ORDINALITY AS " + alias + "(value,ord))"
	}
	if shape.object {
		alias := b.nextAlias()
		item := "CASE"
		for _, key := range shape.keys {
			parameter, err := b.parameter(n, hff.String(key))
			if err != nil {
				return "", err
			}
			value, err := b.normalizedComparisonDocument(n, alias+".value", format, shape.fields[key])
			if err != nil {
				return "", err
			}
			item += " WHEN " + alias + ".key COLLATE \"C\"=" + parameter.code + " COLLATE \"C\" THEN " + value
		}
		otherwise, err := b.normalizedComparisonDocument(n, alias+".value", format, shape.fallback)
		if err != nil {
			return "", err
		}
		if len(shape.keys) == 0 {
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
	leftFormat, leftJSON := b.jsonValues[left.code]
	rightFormat, rightJSON := b.jsonValues[right.code]
	if leftJSON && rightJSON && jsonTaggedScalarComparable(leftType, leftFormat, rightType, rightFormat) {
		return b.dynamicJSONTaggedScalarPair(n, left, leftFormat, right, rightFormat, op, leftType, rightType)
	}
	for i := 0; i < 2; i++ {
		child, _ := n.Child(i)
		if jsonHasTaggedString(b.jsonStoredType(child)) {
			return expression{}, b.failure(n, "dynamic JSON comparison cannot preserve Bytes, Duration, or Timestamp type identity")
		}
	}
	values := []expression{left, right}
	stored := []hff.Type{leftType, rightType}
	for i := range values {
		format, ok := b.jsonValues[values[i].code]
		if !ok {
			return expression{}, b.failure(n, "dynamic comparison requires recoverable JSON representations")
		}
		if err := b.checkComparisonDocumentSchema(n, format, stored[i]); err != nil {
			return expression{}, err
		}
	}
	if !sharedJSONContainer(leftNode.Type(), rightNode.Type()) || (op != hff.Equal && op != hff.NotEqual) {
		return b.scalarJSONPair(n, left, leftFormat, leftType, right, rightFormat, rightType, op)
	}
	if !makeComparisonShape(leftType).any && !makeComparisonShape(rightType).any {
		code, err := b.schemaJSONEqual(n, left.code, leftFormat, leftType, leftNode.Type(), right.code, rightFormat, rightType, rightNode.Type(), both(left.present, right.present), op)
		return expression{code, "TRUE", hff.BoolType}, err
	}
	for i := range values {
		format, ok := b.jsonValues[values[i].code]
		if !ok {
			return expression{}, b.failure(n, "dynamic comparison requires recoverable JSON representations")
		}
		child, _ := n.Child(i)
		code, err := b.normalizedComparisonDocument(n, values[i].code, format, b.jsonStoredType(child))
		if err != nil {
			return expression{}, err
		}
		values[i].code = b.project(code)
	}
	left.code, right.code = values[0].code, values[1].code
	lc, rc := "json_typeof("+left.code+")", "json_typeof("+right.code+")"
	ln, rn := "("+left.code+" #>> '{}'::text[])", "("+right.code+" #>> '{}'::text[])"
	code := "CASE WHEN NOT (" + both(left.present, right.present) + ") OR " + left.code + " IS NULL OR " + right.code + " IS NULL OR " + lc + "='null' OR " + rc + "='null' THEN FALSE"
	// The compiled operand types constrain reachable runtime categories.
	// Keep stored-type normalization and representability checks above, but
	// do not build exact numeric or recursive kernels for impossible pairs.
	leftCompiled, rightCompiled := leftNode.Type(), rightNode.Type()
	for _, kind := range []hff.TypeKind{hff.StringType, hff.BoolType} {
		if !acceptsKind(leftCompiled, kind) || !acceptsKind(rightCompiled, kind) ||
			(kind == hff.BoolType && op != hff.Equal && op != hff.NotEqual) {
			continue
		}
		comparison := b.scalarComparison(expression{jsonScalarCast(ln, kind), "TRUE", kind}, expression{jsonScalarCast(rn, kind), "TRUE", kind}, op)
		code += " WHEN " + both(lc+"='"+jsonCategory(kind)+"'", rc+"='"+jsonCategory(kind)+"'") + " THEN " + comparison.code
	}
	leftInt, leftFloat := jsonNumericCandidates(leftCompiled)
	rightInt, rightFloat := jsonNumericCandidates(rightCompiled)
	if (leftInt || leftFloat) && (rightInt || rightFloat) {
		code += " WHEN " + both(lc+"='number'", rc+"='number'") + " THEN " + b.dynamicJSONNumbers(ln, rn, op, leftInt, leftFloat, rightInt, rightFloat)
	}
	if op == hff.Equal || op == hff.NotEqual {
		var containers []string
		if acceptsKind(leftCompiled, hff.ListType) && acceptsKind(rightCompiled, hff.ListType) {
			containers = append(containers, both(lc+"='array'", rc+"='array'"))
		}
		object := func(typ hff.Type) bool {
			return acceptsKind(typ, hff.StructType) || acceptsKind(typ, hff.MapType)
		}
		if object(leftCompiled) && object(rightCompiled) {
			containers = append(containers, both(lc+"='object'", rc+"='object'"))
		}
		if len(containers) > 0 {
			equal := b.dynamicJSONTreeEqual(left.code, right.code)
			if op == hff.NotEqual {
				equal = "NOT " + equal
			}
			code += " WHEN (" + strings.Join(containers, " OR ") + ") THEN " + equal
		}
	}
	return expression{"(" + code + " ELSE NULL::boolean END)", "TRUE", hff.BoolType}, nil
}

// Tagged Bytes and temporal values occupy the JSON string category, separately
// from numeric and Boolean values. Native JSON spelling distinguishes Int and
// Float.
func (b *builder) dynamicJSONTaggedScalarPair(n hff.Node, left expression, lf JSONFormat, right expression, rf JSONFormat, op hff.Operator, leftType, rightType hff.Type) (expression, error) {
	lc := string(lf) + "_typeof(" + left.code + ")"
	rc := string(rf) + "_typeof(" + right.code + ")"
	lt := "(" + left.code + " #>> '{}'::text[])"
	rt := "(" + right.code + " #>> '{}'::text[])"
	stringKind := hff.DurationType
	parse := b.parseDurationInline
	if jsonRootHasKind(leftType, hff.TimestampType) || jsonRootHasKind(rightType, hff.TimestampType) {
		stringKind = hff.TimestampType
		parse = jsonTimestampNanoseconds
	} else if jsonRootHasKind(leftType, hff.BytesType) || jsonRootHasKind(rightType, hff.BytesType) {
		stringKind = hff.BytesType
		parse = postgresDecodeBase64
	}
	stringValue := "NULL::boolean"
	if stringKind != hff.BytesType || op == hff.Equal || op == hff.NotEqual {
		stringValue = b.scalarComparison(
			expression{parse(lt), "TRUE", stringKind},
			expression{parse(rt), "TRUE", stringKind}, op).code
	}
	code := "(CASE WHEN NOT (" + both(left.present, right.present) + ") OR " + left.code + " IS NULL OR " + right.code + " IS NULL" +
		" THEN FALSE WHEN " + both(lc+"='string'", rc+"='string'") + " THEN " + stringValue
	leftInt, leftFloat := jsonNumericCandidates(leftType)
	rightInt, rightFloat := jsonNumericCandidates(rightType)
	if (leftInt || leftFloat) && (rightInt || rightFloat) {
		// Mixed numeric comparison may add LATERAL projections outside this
		// CASE. Guard the cast inputs before they can see tagged strings.
		leftNumber := "(CASE WHEN " + lc + "='number' THEN " + lt + " END)"
		rightNumber := "(CASE WHEN " + rc + "='number' THEN " + rt + " END)"
		var numeric string
		if (leftInt && leftFloat) || (rightInt && rightFloat) {
			// Classify by spelling only where the schema permits both numeric
			// subtypes. A schema-known Float can be stored in JSONB without a
			// decimal marker and must never be cast to bigint.
			numeric = b.dynamicJSONNumbers(leftNumber, rightNumber, op, leftInt, leftFloat, rightInt, rightFloat)
		} else {
			leftKind, rightKind := hff.IntType, hff.IntType
			if leftFloat {
				leftKind = hff.FloatType
			}
			if rightFloat {
				rightKind = hff.FloatType
			}
			numeric = b.scalarComparison(
				expression{jsonScalarCast(leftNumber, leftKind), "TRUE", leftKind},
				expression{jsonScalarCast(rightNumber, rightKind), "TRUE", rightKind}, op).code
		}
		code += " WHEN " + both(lc+"='number'", rc+"='number'") + " THEN " + numeric
	}
	if op == hff.Equal || op == hff.NotEqual {
		boolean := b.scalarComparison(
			expression{jsonScalarCast(lt, hff.BoolType), "TRUE", hff.BoolType},
			expression{jsonScalarCast(rt, hff.BoolType), "TRUE", hff.BoolType}, op)
		code += " WHEN " + both(lc+"='boolean'", rc+"='boolean'") + " THEN " + boolean.code
	}
	code += " ELSE NULL::boolean END)"
	return expression{code, "TRUE", hff.BoolType}, nil
}

// A tagged scalar union may be compared with another compatible union or
// with a single scalar. JSONB numeric ambiguity is accepted only after type
// metadata has restored native JSON spelling.
func jsonTaggedScalarComparable(left hff.Type, lf JSONFormat, right hff.Type, rf JSONFormat) bool {
	leftTag, leftOK := jsonTaggedScalarFamily(left, lf)
	rightTag, rightOK := jsonTaggedScalarFamily(right, rf)
	return leftOK && rightOK && (leftTag != 0 || rightTag != 0) &&
		(leftTag == 0 || rightTag == 0 || leftTag == rightTag)
}

func jsonTaggedScalarFamily(typ hff.Type, format JSONFormat) (hff.TypeKind, bool) {
	members := []hff.Type{typ}
	if typ.Kind() == hff.UnionType {
		members = typ.Members()
	}
	var tagged hff.TypeKind
	integer, floating := false, false
	for _, member := range members {
		switch member.Kind() {
		case hff.BytesType, hff.TimestampType, hff.DurationType:
			if tagged != 0 && tagged != member.Kind() {
				return 0, false
			}
			tagged = member.Kind()
		case hff.IntType:
			integer = true
		case hff.FloatType:
			floating = true
		case hff.BoolType, hff.NullType:
		default:
			return 0, false
		}
	}
	return tagged, !(integer && floating && format != JSON)
}

type jsonNumericSide struct {
	integer, floating, ambiguous bool
}

func (b *builder) dynamicJSONNumbers(left, right string, op hff.Operator, leftInt, leftFloat, rightInt, rightFloat bool) string {
	return b.jsonNumericPair(left, right, op,
		jsonNumericSide{leftInt, leftFloat, leftInt && leftFloat},
		jsonNumericSide{rightInt, rightFloat, rightInt && rightFloat})
}

// Decode each reachable numeric family once in this scalar subquery. A mixed
// comparison references its Float several times, and ambiguous family pairs
// reuse the same conversions in multiple branches. Never hoist these guarded
// casts out of the numeric caller's CASE or callback scope.
func (b *builder) jsonNumericPair(left, right string, op hff.Operator, l, r jsonNumericSide) string {
	return b.decodedJSONNumericPair(left, right, op, l, r, "", "")
}

func (b *builder) decodedJSONNumericPair(left, right string, op hff.Operator, l, r jsonNumericSide, leftFloat, rightFloat string) string {
	outer, paths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outer, paths }()
	type family struct {
		condition string
		value     expression
	}
	count := func(side jsonNumericSide) int {
		n := 0
		if side.integer {
			n++
		}
		if side.floating {
			n++
		}
		return n
	}
	shared := count(l)*count(r) > 1 || (l.integer && r.floating) || (l.floating && r.integer)
	if shared {
		left, right = b.project(left), b.project(right)
	}
	families := func(text string, side jsonNumericSide, floating string) []family {
		var values []family
		for _, candidate := range []struct {
			kind    hff.TypeKind
			allowed bool
		}{{hff.IntType, side.integer}, {hff.FloatType, side.floating}} {
			if !candidate.allowed {
				continue
			}
			condition := "TRUE"
			if side.ambiguous {
				operator := "!~"
				if candidate.kind == hff.FloatType {
					operator = "~"
				}
				condition = text + " " + operator + " '[.eE]'"
			}
			code := "(CASE WHEN " + condition + " THEN " + jsonScalarCast(text, candidate.kind) + " ELSE NULL END)"
			if candidate.kind == hff.FloatType && floating != "" {
				code = floating
			}
			if shared {
				code = b.project(code)
			}
			values = append(values, family{condition, expression{code, "TRUE", candidate.kind}})
		}
		return values
	}
	leftFamilies, rightFamilies := families(left, l, leftFloat), families(right, r, rightFloat)
	var branches []string
	for _, leftFamily := range leftFamilies {
		for _, rightFamily := range rightFamilies {
			comparison := b.scalarComparison(leftFamily.value, rightFamily.value, op)
			branches = append(branches, "WHEN "+both(leftFamily.condition, rightFamily.condition)+" THEN "+comparison.code)
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
	equal := "CASE WHEN " + lv + " IS NULL OR " + rv + " IS NULL THEN FALSE WHEN " + lc + "<>" + rc + " THEN FALSE WHEN " + lc + " IN ('null','array','object') THEN TRUE WHEN " + lc + "='number' THEN " + b.dynamicJSONNumbers(lt, rt, hff.Equal, true, true, true, true) + " ELSE " + lt + " COLLATE \"C\"=" + rt + " COLLATE \"C\" END"
	return "(WITH RECURSIVE " + walk(l, left) + "," + walk(r, right) + " SELECT NOT EXISTS(SELECT 1 FROM " + l + " FULL JOIN " + r + " ON " + l + ".path COLLATE \"C\"=" + r + ".path COLLATE \"C\" WHERE " + l + ".path IS NULL OR " + r + ".path IS NULL OR NOT (" + equal + ")))"
}
