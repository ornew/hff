package sql

import (
	"fmt"
	"strings"

	"github.com/ornew/hff"
)

// JSONFormat describes the physical PostgreSQL column, not a query syntax.
type JSONFormat string

const (
	// JSON selects PostgreSQL's native json physical representation.
	JSON JSONFormat = "json"
	// JSONB selects PostgreSQL's jsonb physical representation.
	JSONB JSONFormat = "jsonb"
)

// BindJSON maps one HFF schema path to a PostgreSQL json or jsonb column. The
// declared HFF schema supplies scalar subtypes; the caller must store documents
// valid for that schema and selected format. Build rejects operations whose
// distinctions cannot be recovered from the stored representation. Options
// configure metadata for distinctions that JSONB does not preserve.
func BindJSON(path hff.Path, column ColumnRef, format JSONFormat, options ...JSONBindingOption) Binding {
	b := Bind(path, column)
	b.format = format
	for _, option := range options {
		if option.apply == nil {
			b.invalidJSONOption = true
			continue
		}
		option.apply(&b)
	}
	return b
}
func jsonKind(t hff.Type) (hff.TypeKind, bool) {
	if t.Kind() == hff.NeverType {
		return hff.NullType, true
	}
	if t.Kind() == hff.UnionType {
		kind := hff.NullType
		members := 0
		for _, member := range t.Members() {
			if member.Kind() == hff.NullType {
				continue
			}
			members++
			if members > 1 {
				return hff.InvalidType, false
			}
			k, ok := jsonKind(member)
			if !ok || (kind != hff.NullType && kind != k) {
				return hff.InvalidType, false
			}
			kind = k
		}
		return kind, true
	}
	switch t.Kind() {
	case hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType, hff.ListType, hff.MapType, hff.StructType:
		return t.Kind(), true
	}
	return hff.InvalidType, false
}
func (b *builder) jsonDecode(n hff.Node, code string, format JSONFormat, typ hff.Type, present string) (expression, error) {
	kind, ok := jsonKind(typ)
	if !ok {
		if typ.Kind() != hff.AnyType && typ.Kind() != hff.UnionType {
			return expression{}, b.failure(n, "JSON value requires a supported schema type")
		}
		raw := b.project(code)
		normalized := "(CASE WHEN " + string(format) + "_typeof(" + raw + ") = 'null' THEN NULL ELSE " + raw + " END)"
		b.jsonValues[normalized] = format
		return expression{normalized, present, hff.AnyType}, nil
	}
	raw := b.project(code)
	normalized := "(CASE WHEN " + string(format) + "_typeof(" + raw + ") = 'null' THEN NULL ELSE " + raw + " END)"
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
func (b *builder) hasJSONAncestor(n hff.Node) bool {
	for n.Valid() {
		if binding, ok := n.Binding(); ok {
			if _, exists := b.parameters[binding.ID()]; exists {
				return true
			}
		}
		if path, ok := n.Path(); ok {
			for size := len(path); size > 0; size-- {
				if _, yes := b.jsonBindings[pathKey(path[:size])]; yes {
					return true
				}
			}
		}
		n, _ = n.Child(0)
	}
	return false
}
func (b *builder) jsonPath(n hff.Node) (expression, bool, error) {
	if value, handled, err := b.metadataSubpath(n); handled {
		return value, true, err
	}
	if path, ok := n.Path(); ok {
		if format, yes := b.jsonBindings[pathKey(path)]; yes {
			code := b.bindings[pathKey(path)]
			if binding, yes := b.relationBindings[pathKey(path)]; yes {
				converted, err := b.relationJSON(n, binding)
				if err != nil {
					return expression{}, true, err
				}
				code = converted
			}
			if binding, yes := b.structBindings[pathKey(path)]; yes {
				converted, err := b.structJSON(n, binding, n.Type())
				if err != nil {
					return expression{}, true, err
				}
				code = converted
			}
			if b.compositeBindings[pathKey(path)] {
				converted, err := b.compositeJSON(n, code, n.Type())
				if err != nil {
					return expression{}, true, err
				}
				code = converted
			}
			v, err := b.jsonDecode(n, code, format, n.Type(), "TRUE")
			return v, true, err
		}
	}
	if value, handled, err := b.structPath(n); handled {
		return value, true, err
	}
	if value, handled, err := b.compositePath(n); handled {
		return value, true, err
	}
	if n.Kind() != hff.FieldNode || !b.hasJSONAncestor(n) {
		return expression{}, false, nil
	}
	parent, _ := n.Child(0)
	value, err := b.expression(parent)
	if err != nil {
		return expression{}, true, err
	}
	format, ok := b.jsonValues[value.code]
	if !ok {
		return expression{}, true, b.failure(n, "JSON field access requires a JSON object")
	}
	name, _ := n.Field()
	key, err := b.parameter(n, hff.String(name))
	if err != nil {
		return expression{}, true, err
	}
	result, err := b.jsonDecode(n, "("+value.code+" -> "+key.code+")", format, n.Type(), value.present)
	return result, true, err
}

// metadataSubpath prunes direct physical documents only. Guards, callback
// parameters and computed accesses retain their ordinary lowering and scope.
func (b *builder) metadataSubpath(n hff.Node) (expression, bool, error) {
	if n.Kind() != hff.FieldNode {
		return expression{}, false, nil
	}
	eligible := false
	for _, binding := range b.physicalBindings {
		if binding.metadata != nil && binding.format == JSONB {
			eligible = true
			break
		}
	}
	if !eligible {
		return expression{}, false, nil
	}
	root := n
	for root.Kind() == hff.FieldNode {
		root, _ = root.Child(0)
	}
	if root.Kind() != hff.VariableNode {
		return expression{}, false, nil
	}
	path, ok := n.Path()
	if !ok {
		return expression{}, false, nil
	}
	// A more-specific mapping must win, including non-JSON bindings.
	for size := len(path); size > 0; size-- {
		if _, bound := b.bindings[pathKey(path[:size])]; !bound {
			continue
		}
		for _, binding := range b.physicalBindings {
			if pathKey(binding.path) != pathKey(path[:size]) {
				continue
			}
			if size == len(path) || binding.metadata == nil || binding.format != JSONB {
				return expression{}, false, nil
			}
			code, err := quoteColumn(binding.column)
			if err != nil {
				return expression{}, true, err
			}
			metadata, err := b.checkJSONMetadata(binding)
			if err != nil {
				return expression{}, true, err
			}
			pointer := ""
			for _, field := range path[size:] {
				// Keep path lowering linear: the category test and extraction
				// share a projection rather than duplicating a growing expression.
				code = b.project(code)
				key, err := b.parameter(n, hff.String(field))
				if err != nil {
					return expression{}, true, err
				}
				code = "(CASE WHEN jsonb_typeof(" + code + ")='object' THEN " + code + " -> " + key.code + "::text ELSE NULL END)"
				pointer += "/" + strings.ReplaceAll(strings.ReplaceAll(field, "~", "~0"), "/", "~1")
			}
			prefix, err := b.parameter(n, hff.String(pointer))
			if err != nil {
				return expression{}, true, err
			}
			value, err := b.jsonDecode(n, b.restoreJSONMetadataAt(code, metadata, prefix.code+"::text"), JSON, n.Type(), "TRUE")
			return value, true, err
		}
		return expression{}, false, nil
	}
	return expression{}, false, nil
}
func ordinaryPresence(values []expression) string {
	present := "TRUE"
	for i := len(values) - 1; i >= 0; i-- {
		present = both(values[i].present, "("+values[i].code+" IS NULL OR "+present+")")
	}
	return present
}
func (b *builder) jsonIndex(n hff.Node) (expression, error) {
	parent, _ := n.Child(0)
	value, err := b.expression(parent)
	if err != nil {
		return expression{}, err
	}
	format, ok := b.jsonValues[value.code]
	if !ok {
		return expression{}, b.failure(n, "SQL indexing requires a JSON list or object")
	}
	child, _ := n.Child(1)
	index, err := b.expression(child)
	if err != nil {
		return expression{}, err
	}
	present := ordinaryPresence([]expression{value, index})
	var code string
	switch value.kind {
	case hff.ListType:
		if index.kind != hff.IntType && index.kind != hff.FloatType && index.kind != hff.NullType {
			return expression{}, b.failure(n, "JSON list index requires a number")
		}
		valid := validJSONIndex(index)
		code = "(CASE WHEN " + valid + " THEN " + value.code + " -> " + index.code + "::integer ELSE NULL END)"
	case hff.AnyType:
		class := string(format) + "_typeof(" + value.code + ")"
		switch index.kind {
		case hff.StringType:
			code = "(CASE WHEN " + class + "='object' THEN " + value.code + " -> " + index.code + "::text ELSE NULL END)"
		case hff.IntType, hff.FloatType:
			code = "(CASE WHEN " + both("("+class+"='array')", validJSONIndex(index)) + " THEN " + value.code + " -> " + index.code + "::integer ELSE NULL END)"
		case hff.NullType:
			code = "NULL::" + string(format)
		default:
			return expression{}, b.failure(n, "dynamic JSON index requires a statically known string or number")
		}
	case hff.MapType, hff.StructType:
		if index.kind != hff.StringType && index.kind != hff.NullType {
			return expression{}, b.failure(n, "JSON object index requires a string")
		}
		code = "(" + value.code + " -> " + index.code + "::text)"
	default:
		return expression{}, b.failure(n, "JSON indexing requires a list or object")
	}
	return b.jsonDecode(n, code, format, n.Type(), present)
}
func validJSONIndex(index expression) string {
	test := "(" + index.code + " BETWEEN -2147483648 AND 2147483647)"
	if index.kind == hff.FloatType {
		test = both(test, "("+index.code+" = trunc("+index.code+"))")
	}
	return test
}
func (b *builder) jsonCall(n hff.Node, name string, values []expression, format JSONFormat) (expression, error) {
	r := values[0]
	present := ordinaryPresence(values)
	if r.kind == hff.AnyType && (name == "len" || name == "startswith" || name == "endswith") {
		return b.dynamicJSONStringCall(n, name, values)
	}
	if r.kind == hff.AnyType && (name == "not" || name == "and" || name == "or") {
		return b.dynamicJSONBooleanCall(n, name, values)
	}
	if r.kind == hff.AnyType && name != "type" && name != "exists" {
		return expression{}, b.failure(n, "dynamic JSON method requires an explicit supported type guard")
	}
	switch name {
	case "contains", "unique":
		return b.jsonMembership(n, name, values, format)
	case "sort", "concat":
		return b.jsonSequence(n, name, values, format)
	case "exists":
		return boolean(both(r.present, "("+r.code+" IS NOT NULL)")), nil
	case "type":
		if r.kind == hff.AnyType {
			code := "(CASE " + string(format) + "_typeof(" + r.code + ") WHEN 'array' THEN 'list' WHEN 'boolean' THEN 'bool' ELSE COALESCE(" + string(format) + "_typeof(" + r.code + "),'null') END)"
			return expression{code, r.present, hff.StringType}, nil
		}
		typ := "object"
		if r.kind == hff.ListType {
			typ = "list"
		}
		return expression{"(CASE WHEN " + r.code + " IS NULL THEN 'null' ELSE '" + typ + "' END)", r.present, hff.StringType}, nil
	case "size":
		code := string(format) + "_array_length(" + r.code + ")::bigint"
		if r.kind == hff.StructType || r.kind == hff.MapType {
			code = "(CASE WHEN " + r.code + " IS NULL THEN NULL ELSE (SELECT count(*) FROM " + string(format) + "_object_keys(" + r.code + ")) END)"
		}
		return expression{code, present, hff.IntType}, nil
	case "keys", "values", "items":
		if r.kind != hff.StructType && r.kind != hff.MapType {
			return expression{}, b.failure(n, "JSON enumeration requires an object")
		}
		alias := b.nextAlias()
		item := alias + ".key"
		if name == "values" {
			item = alias + ".value"
		} else if name == "items" {
			item = string(format) + "_build_array(" + alias + ".key, " + alias + ".value)"
		}
		code := "(CASE WHEN " + r.code + " IS NULL THEN NULL ELSE (SELECT COALESCE(" + string(format) + "_agg(" + item + " ORDER BY " + alias + ".key COLLATE \"C\"), '[]'::" + string(format) + ") FROM " + string(format) + "_each(" + r.code + ") AS " + alias + ") END)"
		return b.jsonDecode(n, code, format, n.Type(), present)
	case "has":
		arg := values[1]
		code := ""
		if r.kind == hff.ListType {
			if arg.kind != hff.IntType && arg.kind != hff.FloatType && arg.kind != hff.NullType {
				return expression{}, b.failure(n, "JSON list has requires a number")
			}
			// Non-integer indices are data failures; out-of-range integer indices are false.
			integral := "TRUE"
			if arg.kind == hff.FloatType {
				integral = both("("+arg.code+" = trunc("+arg.code+"))", validFloatIndex(arg.code))
			}
			length := string(format) + "_array_length(" + r.code + ")::bigint"
			code = "(CASE WHEN " + r.code + " IS NULL OR " + arg.code + " IS NULL OR NOT " + integral + " THEN NULL ELSE (" + arg.code + " >= -" + length + " AND " + arg.code + " < " + length + ") END)"
		} else {
			if arg.kind != hff.StringType && arg.kind != hff.NullType {
				return expression{}, b.failure(n, "JSON object has requires a string")
			}
			code = "(CASE WHEN " + r.code + " IS NULL OR " + arg.code + " IS NULL THEN NULL ELSE (" + r.code + " -> " + arg.code + "::text IS NOT NULL) END)"
		}
		return expression{code, present, hff.BoolType}, nil
	case "get":
		if r.kind == hff.ListType {
			return expression{}, b.failure(n, "JSON get requires an object")
		}
		arg := values[1]
		if arg.kind != hff.StringType && arg.kind != hff.NullType {
			return expression{}, b.failure(n, "JSON get requires a string")
		}
		return b.jsonDecode(n, "("+r.code+" -> "+arg.code+"::text)", format, n.Type(), present)
	default:
		return expression{}, b.failure(n, "JSON collection method is not yet supported: "+name)
	}
}
func nonnullJSONType(t hff.Type) hff.Type {
	if t.Kind() == hff.UnionType {
		for _, m := range t.Members() {
			if m.Kind() != hff.NullType {
				return m
			}
		}
	}
	return t
}
func (b *builder) jsonTypeGuard(n hff.Node, value expression, format JSONFormat, source, target hff.Type) (expression, error) {
	test, err := b.jsonMatches(n, value.code, format, source, target)
	if err != nil {
		return expression{}, err
	}
	// SQL type tests on NULL may themselves be NULL. No-value presence must be
	// a definite false so later null tests still produce ordinary false.
	present := both(value.present, "COALESCE(("+test+"), FALSE)")
	guarded := "CASE WHEN " + present + " THEN " + value.code + " ELSE NULL END"
	return b.jsonDecode(n, guarded, format, n.Type(), present)
}
func (b *builder) jsonMatches(n hff.Node, code string, format JSONFormat, source, target hff.Type) (string, error) {
	null := "(" + code + " IS NULL OR " + string(format) + "_typeof(" + code + ") = 'null')"
	switch target.Kind() {
	case hff.AnyType:
		return "TRUE", nil
	case hff.NeverType:
		return "FALSE", nil
	case hff.NullType:
		return null, nil
	case hff.UnionType:
		parts := make([]string, len(target.Members()))
		for i, t := range target.Members() {
			test, err := b.jsonMatches(n, code, format, source, t)
			if err != nil {
				return "", err
			}
			parts[i] = test
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
	if source.Kind() == hff.UnionType && (target.Kind() == hff.ListType || target.Kind() == hff.MapType) {
		return b.jsonUnionCollectionMatches(n, code, format, source, target)
	}
	kind, ok := jsonKind(source)
	if !ok {
		if target.Kind() == hff.IntType || target.Kind() == hff.FloatType {
			integer, floating := jsonNumericCandidates(source)
			if !integer || !floating {
				if (target.Kind() == hff.IntType && integer) || (target.Kind() == hff.FloatType && floating) {
					return "(" + string(format) + "_typeof(" + code + ") = 'number')", nil
				}
				return "FALSE", nil
			}
		}
		return b.dynamicJSONMatches(n, code, format, target)
	}
	source = nonnullJSONType(source)
	if target.Kind() == hff.ListType {
		if kind != hff.ListType {
			return "FALSE", nil
		}
		alias := b.nextAlias()
		child := alias + ".value"
		test, err := b.jsonMatches(n, child, format, source.Element(), target.Element())
		if err != nil {
			return "", err
		}
		safe := "(CASE WHEN " + string(format) + "_typeof(" + code + ") = 'array' THEN " + code + " ELSE '[]'::" + string(format) + " END)"
		return both("(NOT "+null+")", "NOT EXISTS (SELECT 1 FROM "+string(format)+"_array_elements("+safe+") AS "+alias+" WHERE NOT ("+test+"))"), nil
	}
	if target.Kind() == hff.MapType {
		if kind != hff.MapType && kind != hff.StructType {
			return "FALSE", nil
		}
		alias := b.nextAlias()
		child := alias + ".value"
		var test string
		if kind == hff.MapType {
			var err error
			test, err = b.jsonMatches(n, child, format, source.Element(), target.Element())
			if err != nil {
				return "", err
			}
		} else {
			parts := []string{}
			for _, field := range source.Fields() {
				key, err := b.parameter(n, hff.String(field.Name))
				if err != nil {
					return "", err
				}
				match, err := b.jsonMatches(n, child, format, field.Type, target.Element())
				if err != nil {
					return "", err
				}
				parts = append(parts, "WHEN "+alias+".key = "+key.code+" COLLATE \"C\" THEN "+match)
			}
			test = "FALSE"
			if len(parts) > 0 {
				test = "(CASE " + strings.Join(parts, " ") + " ELSE FALSE END)"
			}
		}
		safe := "(CASE WHEN " + string(format) + "_typeof(" + code + ") = 'object' THEN " + code + " ELSE '{}'::" + string(format) + " END)"
		return both("(NOT "+null+")", "NOT EXISTS (SELECT 1 FROM "+string(format)+"_each("+safe+") AS "+alias+" WHERE NOT ("+test+"))"), nil
	}
	if target.Kind() != kind {
		return "FALSE", nil
	}
	if kind == hff.ListType || kind == hff.MapType || kind == hff.StructType {
		return "", b.failure(n, fmt.Sprintf("unsupported JSON type guard %s", target.Key()))
	}
	return "(NOT " + null + ")", nil
}
