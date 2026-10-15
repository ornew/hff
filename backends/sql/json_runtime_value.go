package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// A runtime node is [kind,payload]. Container payloads contain runtime nodes,
// so recursive state can grow without growing a compile-time descriptor graph.
// This private carrier is never a stored Binding or a user-visible JSON value.
// No-value remains the owning expression's separate presence bit.
type jsonRuntimeValue struct {
	code  string
	lossy bool
	// Positional types of a private pair; never a raw-JSON carrier proof.
	itemRoles     *jsonItemRoles
	itemListRoles *jsonItemRoles
}

const runtimeNull = `'["null",null]'::json`

func runtimeValue(code string, typ hff.Type, present string, lossy bool) *jsonDeferredValue {
	return &jsonDeferredValue{typ: typ, present: present, runtime: &jsonRuntimeValue{code: code, lossy: lossy}}
}

func runtimeTag(kind hff.TypeKind) string {
	switch kind {
	case hff.BoolType:
		return "bool"
	case hff.IntType:
		return "int"
	case hff.FloatType:
		return "float"
	case hff.StringType:
		return "string"
	case hff.BytesType:
		return "bytes"
	case hff.DurationType:
		return "duration"
	case hff.TimestampType:
		return "timestamp"
	case hff.ListType:
		return "list"
	case hff.StructType, hff.MapType:
		return "object"
	}
	return "null"
}

func runtimeNode(tag, payload string) string {
	return "json_build_array('" + tag + "'::text," + payload + ")"
}

func (b *builder) encodeRuntimeValue(n hff.Node, value *jsonDeferredValue) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if value.runtime != nil {
		return value.runtime.code, nil
	}
	if value.selection != nil {
		result, err := b.resolveDeferredSelection(n, value, hff.AnyType, func(child *jsonDeferredValue) (expression, error) {
			code, err := b.encodeRuntimeValue(n, child)
			return expression{code, child.present, hff.AnyType}, err
		})
		return result.code, err
	}
	if value.choices != nil {
		codes := make([]string, len(value.choices))
		for i, choice := range value.choices {
			var err error
			codes[i], err = b.encodeRuntimeValue(n, choice.value)
			if err != nil {
				return "", err
			}
		}
		return deferredCases(value.choices, codes, runtimeNull), nil
	}
	if value.list != nil {
		plan, err := b.deferredValueList(n, value)
		if err != nil {
			return "", err
		}
		var rows []string
		for _, arm := range plan.arms {
			var code string
			steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
				var err error
				code, err = b.encodeRuntimeValue(n, deferredLeaf(arm.value))
				return err
			})
			if err != nil {
				return "", err
			}
			arm.steps = steps
			if arm.empty {
				continue
			}
			rows = append(rows, "SELECT "+deferredOrdinal(arm)+" AS ord,"+code+" AS value FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
		}
		payload := "'[]'::json"
		if len(rows) > 0 {
			payload = "(SELECT COALESCE(json_agg(value ORDER BY ord),'[]'::json) FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + b.nextAlias() + ")"
		}
		return "(CASE WHEN " + plan.null + " THEN " + runtimeNull + " ELSE " + runtimeNode("list", payload) + " END)", nil
	}
	if value.object != nil {
		var rows []string
		for _, field := range *value.object {
			key, err := b.parameter(n, hff.String(field.name))
			if err != nil {
				return "", err
			}
			code, err := b.encodeRuntimeValue(n, field.value)
			if err != nil {
				return "", err
			}
			rows = append(rows, "SELECT "+key.code+"::text AS key,"+code+" AS value")
		}
		payload := "'{}'::json"
		if len(rows) > 0 {
			payload = "(SELECT json_object_agg(key,value) FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + b.nextAlias() + ")"
		}
		return runtimeNode("object", payload), nil
	}
	parameter := value.leaf
	if parameter.itemSlot != nil {
		return b.jsonItemSlotOperation(n, *parameter.itemSlot, runtimeNull, func(typ hff.Type) (string, error) {
			return b.runtimeDocument(n, parameter.itemSlot.raw, parameter.itemSlot.format, typ)
		})
	}
	carrier, err := b.deferredLeafExpression(n, *parameter)
	if err != nil {
		return "", err
	}
	raw, format, err := b.jsonRepresentation(n, carrier)
	if err != nil {
		return "", err
	}
	if parameter.itemListRoles != nil {
		alias := b.nextAlias()
		pair, err := b.runtimeItemPair(n, alias+".value", format, parameter.itemListRoles)
		if err != nil {
			return "", err
		}
		payload := "(SELECT COALESCE(json_agg(" + pair + " ORDER BY " + alias + ".ord),'[]'::json) FROM " + string(format) + "_array_elements(" + raw + ") WITH ORDINALITY AS " + alias + "(value,ord))"
		return "(CASE WHEN " + raw + " IS NULL OR " + string(format) + "_typeof(" + raw + ")='null' THEN " + runtimeNull + " ELSE " + runtimeNode("list", payload) + " END)", nil
	}
	if parameter.itemRoles != nil {
		return b.runtimeItemPair(n, raw, format, parameter.itemRoles)
	}
	return b.runtimeDocument(n, raw, format, value.typ)
}

func (b *builder) runtimeItemPair(n hff.Node, raw string, format JSONFormat, roles *jsonItemRoles) (string, error) {
	slot := jsonItemSlot{raw: "(" + raw + "->1)", pair: raw, key: "FALSE", value: "TRUE", present: "TRUE", format: format, roles: roles}
	value, err := b.jsonItemSlotOperation(n, slot, runtimeNull, func(typ hff.Type) (string, error) {
		return b.runtimeDocument(n, slot.raw, format, typ)
	})
	if err != nil {
		return "", err
	}
	key := runtimeNode("string", "("+raw+"->0)::json")
	return "(CASE WHEN " + raw + " IS NULL OR " + string(format) + "_typeof(" + raw + ")='null' THEN " + runtimeNull + " ELSE " + runtimeNode("list", "json_build_array("+key+","+value+")") + " END)", nil
}

// Stored documents require a recoverable schema before private tags are added.
// In particular, a JSON-native Any string must never acquire a Bytes tag.
func (b *builder) runtimeDocument(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	// Recursive children refer to aliases local to their array/object scan.
	// Keep normalization projections in that same scalar-subquery scope.
	outer, paths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outer, paths }()
	code, err := b.runtimeDocumentBody(n, raw, format, typ)
	if err != nil || len(b.steps) == 0 {
		return code, err
	}
	from := "(VALUES (1)) AS " + b.nextAlias() + "(n) CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	return "(SELECT " + code + " FROM " + from + ")", nil
}

func (b *builder) runtimeDocumentBody(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	if err := b.checkComparisonDocumentSchema(n, format, typ); err != nil {
		return "", err
	}
	shape := makeComparisonShape(typ)
	if shape.any {
		return b.runtimeNativeDocument(raw, format), nil
	}
	normal, err := b.normalizedComparisonDocument(n, raw, format, typ)
	if err != nil {
		return "", err
	}
	normal = b.project(normal)
	class := "json_typeof(" + normal + ")"
	code := "CASE WHEN " + class + "='null' THEN " + runtimeNull
	for _, member := range shape.members {
		kind := member.Kind()
		if kind == hff.ListType || kind == hff.MapType || kind == hff.StructType || kind == hff.NullType || kind == hff.NeverType {
			continue
		}
		condition := class + "='" + jsonCategory(kind) + "'"
		if shape.integer && shape.floating && (kind == hff.IntType || kind == hff.FloatType) {
			op := "!~"
			if kind == hff.FloatType {
				op = "~"
			}
			condition = both(condition, "("+normal+" #>> '{}') "+op+" '[.eE]'")
		}
		payload := normal
		if jsonCategory(kind) == "string" {
			payload = "to_json(" + normal + " #>> '{}')"
		} else if kind == hff.BoolType {
			payload = "to_json((" + normal + " #>> '{}')::boolean)"
		}
		code += " WHEN " + condition + " THEN " + runtimeNode(runtimeTag(kind), payload)
	}
	if shape.list {
		alias := b.nextAlias()
		child, err := b.runtimeDocument(n, alias+".value", JSON, shape.element)
		if err != nil {
			return "", err
		}
		payload := "(SELECT COALESCE(json_agg(" + child + " ORDER BY " + alias + ".ord),'[]'::json) FROM json_array_elements(" + normal + ") WITH ORDINALITY AS " + alias + "(value,ord))"
		code += " WHEN " + class + "='array' THEN " + runtimeNode("list", payload)
	}
	if shape.object {
		alias := b.nextAlias()
		var branches []string
		for _, key := range shape.keys {
			parameter, err := b.parameter(n, hff.String(key))
			if err != nil {
				return "", err
			}
			child, err := b.runtimeDocument(n, alias+".value", JSON, shape.fields[key])
			if err != nil {
				return "", err
			}
			branches = append(branches, "WHEN "+alias+".key COLLATE \"C\"="+parameter.code+" COLLATE \"C\" THEN "+child)
		}
		child, err := b.runtimeDocument(n, alias+".value", JSON, shape.fallback)
		if err != nil {
			return "", err
		}
		if len(branches) > 0 {
			child = "(CASE " + strings.Join(branches, " ") + " ELSE " + child + " END)"
		}
		payload := "(SELECT COALESCE(json_object_agg(" + alias + ".key," + child + "),'{}'::json) FROM json_each(" + normal + ") AS " + alias + "(key,value))"
		code += " WHEN " + class + "='object' THEN " + runtimeNode("object", payload)
	}
	return "(" + code + " ELSE " + runtimeNull + " END)", nil
}

// JSON-native Any has a finite recursive grammar even when its data is deep.
// A token walk avoids expanding that grammar at compile time or converting to
// jsonb, whose numeric formatting would discard subtype and signed zero.
func (b *builder) runtimeNativeDocument(raw string, format JSONFormat) string {
	walk, child, tokens := b.nextAlias(), b.nextAlias(), b.nextAlias()
	value := "COALESCE(" + raw + "::json,'null'::json)"
	text := "(value #>> '{}')"
	integer, floating := jsonScalarCast(text, hff.IntType), jsonScalarCast(text, hff.FloatType)
	numeric := "(CASE WHEN " + text + " ~ '[.eE]' THEN CASE WHEN " + floating + " IS NULL THEN " + runtimeNull + " ELSE " + runtimeNode("float", b.floatText(floating)+"::json") + " END ELSE CASE WHEN " + integer + " IS NULL THEN " + runtimeNull + " ELSE " + runtimeNode("int", "to_json("+integer+")") + " END END)::text"
	return `(WITH RECURSIVE ` + walk + `(value,position,prefix) AS (
 SELECT ` + value + `,ARRAY[]::bigint[],''::text
 UNION ALL SELECT ` + child + `.value,` + walk + `.position || (` + child + `.ord*2),
 (CASE WHEN ` + child + `.ord>1 THEN ',' ELSE '' END)||` + child + `.prefix
 FROM ` + walk + ` CROSS JOIN LATERAL (
 SELECT a.value,a.ord,''::text AS prefix FROM json_array_elements(CASE WHEN json_typeof(` + walk + `.value)='array' THEN ` + walk + `.value ELSE '[]'::json END) WITH ORDINALITY AS a(value,ord)
 UNION ALL SELECT o.value,row_number() OVER(ORDER BY o.key COLLATE "C"),to_json(o.key)::text||':' FROM json_each(CASE WHEN json_typeof(` + walk + `.value)='object' THEN ` + walk + `.value ELSE '{}'::json END) AS o(key,value)
 ) AS ` + child + `
 ),` + tokens + ` AS (
 SELECT position||0::bigint AS position,prefix||CASE json_typeof(value)
 WHEN 'array' THEN '["list",[' WHEN 'object' THEN '["object",{'
 WHEN 'boolean' THEN json_build_array('bool'::text,(value #>> '{}')::boolean)::text
 WHEN 'string' THEN json_build_array('string'::text,(value #>> '{}'))::text
 WHEN 'number' THEN ` + numeric + ` ELSE '["null",null]' END AS token FROM ` + walk + `
 UNION ALL SELECT position||9223372036854775807::bigint,CASE json_typeof(value) WHEN 'array' THEN ']]' ELSE '}]' END FROM ` + walk + ` WHERE json_typeof(value) IN ('array','object')
 ) SELECT string_agg(token,'' ORDER BY position)::json FROM ` + tokens + `)`
}

func (b *builder) runtimeValueList(n hff.Node, value *jsonDeferredValue) jsonDeferredList {
	code := value.runtime.code
	alias := b.nextAlias()
	item := runtimeValue(alias+".value", jsonStoredMemberType(sortListType(value.typ), "", false, true), "TRUE", value.runtime.lossy)
	item.runtime.itemRoles = value.runtime.itemListRoles
	return jsonDeferredList{null: "(" + code + "->>0<>'list')", present: value.present, arms: []jsonDeferredArm{{
		from:    "json_array_elements(CASE WHEN " + code + "->>0='list' THEN " + code + "->1 ELSE '[]'::json END) WITH ORDINALITY AS " + alias + "(value,ord)",
		ordinal: []string{alias + ".ord"}, selected: "TRUE", value: deferredValueParameter(item), parameters: copyJSONParameters(b.parameters),
	}}}
}

func (b *builder) accessRuntimeValue(n hff.Node, value *jsonDeferredValue, index *deferredAccessIndex) (*jsonDeferredValue, error) {
	operand, err := b.lowerDeferredIndex(index)
	if err != nil {
		return nil, err
	}
	code := value.runtime.code
	tag, payload := "("+code+"->>0)", "("+code+"->1)"
	var key, position string
	if operand.kind == hff.StringType {
		key = operand.code
	} else if operand.kind == hff.AnyType {
		raw, format, err := b.jsonRepresentation(index.node, operand)
		if err != nil {
			return nil, err
		}
		key = "(CASE WHEN " + b.jsonStringMatch(index.node, raw, format) + " THEN " + raw + " #>> '{}' ELSE NULL::text END)"
		// Indexing needs the decoded value, not the original Int/Float
		// subtype. Every usable int32 index is exact in Float; floating input
		// must round before its integrality test, just as ordinary decoding.
		if !index.knownKey {
			text := "(" + raw + " #>> '{}')"
			number := b.project("(CASE WHEN " + string(format) + "_typeof(" + raw + ")='number' THEN " + jsonFloatCast(text) + " ELSE NULL::double precision END)")
			position = "(CASE WHEN " + number + "=trunc(" + number + ") AND " + number + " BETWEEN -2147483648 AND 2147483647 THEN " + number + "::integer ELSE NULL::integer END)"
		}
	}
	method, call := n.Method()
	var numeric jsonNumericOperand
	if operand.kind != hff.AnyType && !(call && method.Name() == "get") && !index.knownKey && acceptsKind(value.typ, hff.ListType) {
		numeric, err = b.numericOperand(index.node, operand)
		if err != nil {
			return nil, err
		}
	}
	var positions []string
	if numeric.integerPossible {
		positions = append(positions, "WHEN "+numeric.isInteger+" AND "+numeric.integer.code+" BETWEEN -2147483648 AND 2147483647 THEN "+numeric.integer.code+"::integer")
	}
	if numeric.floatPossible {
		positions = append(positions, "WHEN "+numeric.isFloat+" AND "+numeric.floating.code+"=trunc("+numeric.floating.code+") AND "+numeric.floating.code+" BETWEEN -2147483648 AND 2147483647 THEN "+numeric.floating.code+"::integer")
	}
	if len(positions) > 0 {
		position = "(CASE " + strings.Join(positions, " ") + " ELSE NULL::integer END)"
	}
	result := "CASE"
	if key != "" {
		result += " WHEN " + tag + "='object' THEN " + payload + "->(" + key + ")"
	}
	if position != "" && !(call && method.Name() == "get") {
		result += " WHEN " + tag + "='list' THEN " + payload + "->" + position
	}
	if result == "CASE" {
		result = "NULL::json"
	} else {
		result = "(" + result + " ELSE NULL::json END)"
	}
	present := both(value.present, "("+tag+"='null' OR "+operand.present+")")
	if call && method.Name() == "get" {
		present = both(value.present, "("+tag+"<>'object' OR "+operand.present+")")
	}
	stored := jsonStoredMemberType(value.typ, index.key, index.knownKey, n.Kind() != hff.FieldNode)
	if roles := value.runtime.itemRoles; roles != nil && index.knownPosition {
		position := index.position
		if position < 0 {
			position += 2
		}
		if position == 0 {
			stored = intersectItemType(stored, hff.PrimitiveType(hff.StringType))
		} else if position == 1 {
			stored = intersectItemType(stored, roles.value)
		}
	}
	return runtimeValue(b.project("COALESCE("+result+","+runtimeNull+")"), stored, present, value.runtime.lossy && deferredTypeMayFloat(stored)), nil
}

func (b *builder) runtimeMatches(n hff.Node, code string, target hff.Type) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if target.Kind() == hff.AnyType {
		return "TRUE", nil
	}
	if target.Kind() == hff.NeverType {
		return "FALSE", nil
	}
	if target.Kind() == hff.UnionType {
		var tests []string
		for _, member := range target.Members() {
			test, err := b.runtimeMatches(n, code, member)
			if err != nil {
				return "", err
			}
			tests = append(tests, test)
		}
		return "(" + strings.Join(tests, " OR ") + ")", nil
	}
	tag := "(" + code + "->>0)"
	test := "(" + tag + "='" + runtimeTag(target.Kind()) + "')"
	if target.Kind() == hff.ListType || target.Kind() == hff.MapType {
		alias := b.nextAlias()
		child, err := b.runtimeMatches(n, alias+".value", target.Element())
		if err != nil {
			return "", err
		}
		from := "json_array_elements(CASE WHEN " + tag + "='list' THEN " + code + "->1 ELSE '[]'::json END) AS " + alias + "(value)"
		if target.Kind() == hff.MapType {
			from = "json_each(CASE WHEN " + tag + "='object' THEN " + code + "->1 ELSE '{}'::json END) AS " + alias + "(key,value)"
		}
		test = both(test, "NOT EXISTS(SELECT 1 FROM "+from+" WHERE NOT COALESCE("+child+",FALSE))")
	}
	return test, nil
}

// Only a concrete recoverable target may leave the typed carrier. Dynamic
// observers and collection paths consume the descriptor directly instead.
func (b *builder) materializeRuntimeValue(n hff.Node, value *jsonDeferredValue) (expression, error) {
	kind, known := jsonKind(value.typ)
	if !runtimeSchemaRecoverable(value.typ) {
		return expression{}, b.failure(n, "typed fold value requires an explicit type guard for this operation")
	}
	if !known || kind == hff.ListType || kind == hff.MapType || kind == hff.StructType {
		if err := b.checkJSONStringSchemas(n, value.typ); err != nil {
			return expression{}, err
		}
		raw := b.runtimeJSON(value.runtime.code)
		return b.jsonDecode(n, "("+raw+")::json", JSON, value.typ, value.present)
	}
	return b.jsonDecode(n, "("+value.runtime.code+"->1)", JSON, value.typ, value.present)
}

func runtimeSchemaRecoverable(typ hff.Type) bool {
	if typ.Kind() == hff.AnyType {
		return false
	}
	if typ.Kind() == hff.ListType || typ.Kind() == hff.MapType {
		return runtimeSchemaRecoverable(typ.Element())
	}
	for _, field := range typ.Fields() {
		if !runtimeSchemaRecoverable(field.Type) {
			return false
		}
	}
	for _, member := range typ.Members() {
		if !runtimeSchemaRecoverable(member) {
			return false
		}
	}
	return true
}

func (b *builder) observeRuntimeValue(n hff.Node, value *jsonDeferredValue, name string) (expression, error) {
	code := value.runtime.code
	tag, payload := "("+code+"->>0)", "("+code+"->1)"
	size := "(CASE WHEN " + tag + "='list' THEN json_array_length(" + payload + ")::bigint WHEN " + tag + "='object' THEN (SELECT count(*) FROM json_object_keys(" + payload + ")) ELSE NULL::bigint END)"
	switch name {
	case "type":
		return expression{"(CASE WHEN " + tag + " IN ('int','float') THEN 'number' ELSE " + tag + " END)", value.present, hff.StringType}, nil
	case "exists":
		return boolean(both(value.present, "("+tag+"<>'null')")), nil
	case "len":
		text := "(" + payload + " #>> '{}')"
		code := "(CASE WHEN " + tag + "='string' THEN char_length(" + text + ")::bigint WHEN " + tag + "='bytes' THEN octet_length(" + postgresDecodeBase64(text) + ")::bigint ELSE NULL::bigint END)"
		return expression{code, value.present, hff.IntType}, nil
	case "size", "count":
		if name == "count" {
			size = "(CASE WHEN " + tag + "='list' THEN json_array_length(" + payload + ")::bigint ELSE NULL::bigint END)"
		}
		return expression{size, value.present, hff.IntType}, nil
	case "truth":
		text := "(" + payload + " #>> '{}')"
		truth := "(CASE " + tag + " WHEN 'null' THEN FALSE WHEN 'bool' THEN " + text + "::boolean WHEN 'int' THEN " + text + "::bigint<>0 WHEN 'float' THEN " + text + "::double precision<>0 WHEN 'string' THEN " + text + "<>'' WHEN 'bytes' THEN " + text + "<>'' WHEN 'duration' THEN " + text + "<>'0s' WHEN 'list' THEN " + size + ">0 WHEN 'object' THEN " + size + ">0 ELSE TRUE END)"
		return boolean(both(value.present, truth)), nil
	case "json", "string":
		if value.runtime.lossy && deferredTypeMayFloat(value.typ) {
			return expression{}, b.failure(n, "JSONB cannot preserve signed zero for float serialization")
		}
		out := b.runtimeJSON(code)
		if name == "string" {
			out = "(CASE WHEN " + tag + " IN ('string','bytes','duration','timestamp') THEN " + payload + " #>> '{}' ELSE " + out + " END)"
		}
		return expression{out, value.present, hff.StringType}, nil
	}
	return expression{}, b.failure(n, "unsupported typed fold value observer")
}

// Emit canonical public JSON by walking typed containers. Scalar payloads
// already crossed their original schema's normalization boundary.
func (b *builder) runtimeJSON(code string) string {
	walk, child, tokens := b.nextAlias(), b.nextAlias(), b.nextAlias()
	return `(WITH RECURSIVE ` + walk + `(value,position,prefix) AS (
 SELECT ` + code + `,ARRAY[]::bigint[],''::text
 UNION ALL SELECT ` + child + `.value,` + walk + `.position||(` + child + `.ord*2),(CASE WHEN ` + child + `.ord>1 THEN ',' ELSE '' END)||` + child + `.prefix
 FROM ` + walk + ` CROSS JOIN LATERAL (
 SELECT a.value,a.ord,''::text AS prefix FROM json_array_elements(CASE WHEN ` + walk + `.value->>0='list' THEN ` + walk + `.value->1 ELSE '[]'::json END) WITH ORDINALITY AS a(value,ord)
 UNION ALL SELECT o.value,row_number() OVER(ORDER BY o.key COLLATE "C"),to_json(o.key)::text||':' FROM json_each(CASE WHEN ` + walk + `.value->>0='object' THEN ` + walk + `.value->1 ELSE '{}'::json END) AS o(key,value)
 ) AS ` + child + `
 ),` + tokens + ` AS (
 SELECT position||0::bigint AS position,prefix||CASE value->>0 WHEN 'list' THEN '[' WHEN 'object' THEN '{' ELSE COALESCE((value->1)::text,'null') END AS token FROM ` + walk + `
 UNION ALL SELECT position||9223372036854775807::bigint,CASE value->>0 WHEN 'list' THEN ']' ELSE '}' END FROM ` + walk + ` WHERE value->>0 IN ('list','object')
 ) SELECT string_agg(token,'' ORDER BY position) FROM ` + tokens + `)`
}
