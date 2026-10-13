package sql

import (
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
)

// SQLPredicate is an immutable PostgreSQL Boolean expression used for explicit
// parent-state and relation conditions. Prefer typed constructors such as
// Equal, IsNull, And, and AsPredicate: they quote identifiers and bind literal
// values. TrustedSQL is an escape hatch for developer-authored SQL and is not
// parsed or proven equivalent to HFF by this package.
type SQLPredicate struct {
	code       string
	references []ColumnRef
	node       *predicateNode
}

// TrustedSQL wraps a developer-authored PostgreSQL Boolean expression. It is
// not parsed, type-checked, sanitized, or proven deterministic or
// side-effect-free. Use only fixed trusted source text, never query or record
// data. Supply referenced identifiers as ColumnRefs for alias tracking; this
// does not validate that the text uses those references or is a predicate.
func TrustedSQL(code string, references ...ColumnRef) SQLPredicate {
	return SQLPredicate{code: code, references: append([]ColumnRef(nil), references...)}
}

type structBinding struct {
	present, null SQLPredicate
	fields        []Binding
}

// BindStruct maps one HFF struct at path to separate columns and explicit
// parent-state predicates. present is true when the logical object exists;
// null is true when that present object is logically null. Neither state is
// inferred from child columns. Each child Binding path is exactly one literal
// field name relative to this struct; nest BindStruct for nested parent states.
// Unbound optional fields are absent, while required fields need a binding. A
// bound scalar SQL NULL means an explicit null field, never omission. The caller
// owns the truth and deterministic behavior of both predicates and the
// schema/data contract. BindTimestamp may map a Timestamp child for direct
// field predicates, but materializing the whole struct with that child is not
// yet supported.
func BindStruct(path hff.Path, present, null SQLPredicate, fields ...Binding) Binding {
	return Binding{path: append(hff.Path(nil), path...), structure: &structBinding{
		present: present, null: null, fields: append([]Binding(nil), fields...),
	}}
}

func structSchema(typ hff.Type) (hff.Type, bool) {
	kind, ok := jsonKind(typ)
	return nonnullJSONType(typ), ok && kind == hff.StructType
}

func (b *builder) checkPredicate(predicate SQLPredicate) error {
	if predicate.node != nil {
		validation := *b
		validation.args = nil
		validation.predicates = nil
		_, err := validation.compilePredicate(predicate)
		return err
	}
	if strings.TrimSpace(predicate.code) == "" || !utf8.ValidString(predicate.code) || strings.ContainsRune(predicate.code, 0) {
		return configuration("struct presence and null expressions must be nonempty trusted Boolean SQL")
	}
	for _, reference := range predicate.references {
		if _, err := quoteColumn(reference); err != nil {
			return err
		}
		for _, part := range reference.parts {
			b.reserved[part] = true
		}
	}
	return nil
}

func (b *builder) checkStructBinding(binding Binding, typ hff.Type, kinds map[string]hff.TypeKind, formats map[string]JSONFormat, types map[string]string) error {
	if err := checkContext(b.ctx); err != nil {
		return err
	}
	schema, ok := structSchema(typ)
	if !ok {
		return configuration("flattened struct binding requires an unambiguous struct schema")
	}
	if err := b.checkPredicate(binding.structure.present); err != nil {
		return err
	}
	if err := b.checkPredicate(binding.structure.null); err != nil {
		return err
	}
	declared := map[string]hff.Field{}
	for _, field := range schema.Fields() {
		declared[field.Name] = field
	}
	seen := map[string]bool{}
	for _, child := range binding.structure.fields {
		if len(child.path) != 1 {
			return configuration("struct field bindings require one relative literal name")
		}
		name := child.path[0]
		field, exists := declared[name]
		if !exists || seen[name] {
			return configuration("undeclared or duplicate struct field binding: " + name)
		}
		seen[name] = true
		if child.relation != nil {
			if err := b.checkRelation(child, field.Type, kinds, formats, types); err != nil {
				return err
			}
			continue
		}
		if child.structure != nil {
			if err := b.checkStructBinding(child, field.Type, kinds, formats, types); err != nil {
				return err
			}
			continue
		}
		if child.timestamp {
			if !timestampSchema(field.Type) || child.format != "" || child.composite || child.metadata != nil {
				return configuration("timestamp struct field requires one Timestamp type and native columns")
			}
			if _, err := b.timestampBindingCode(child, formats, kinds); err != nil {
				return err
			}
			continue
		}
		if timestampSchema(field.Type) {
			return configuration("Timestamp struct fields require BindTimestamp")
		}
		if _, err := b.checkJSONMetadata(child); err != nil {
			return err
		}
		column, err := quoteColumn(child.column)
		if err != nil {
			return err
		}
		format := child.format
		if child.composite {
			if _, ok := structSchema(field.Type); !ok {
				return configuration("composite field binding requires a struct schema")
			}
			format = "composite"
		} else if format != "" && format != JSON && format != JSONB {
			return configuration("invalid JSON binding format")
		}
		kind, known := jsonKind(field.Type)
		if !known && format == "" && !child.composite {
			kind, known = scalarKind(field.Type)
		}
		if !known && format == "" {
			return configuration("struct field requires a supported physical schema")
		}
		if previous, exists := formats[column]; exists && previous != format {
			return configuration("SQL column has conflicting physical formats")
		}
		formats[column] = format
		if format != "" || kind == hff.ListType || kind == hff.StructType || kind == hff.MapType {
			if previous, exists := types[column]; exists && previous != field.Type.Key() {
				return configuration("SQL column has conflicting schema types")
			}
			types[column] = field.Type.Key()
		}
		if format == "" {
			if previous, exists := kinds[column]; exists && previous != kind {
				return configuration("SQL column has conflicting schema types")
			}
			kinds[column] = kind
		}
		for _, part := range child.column.parts {
			b.reserved[part] = true
		}
	}
	for _, field := range schema.Fields() {
		if !field.Optional && !seen[field.Name] {
			return configuration("required struct field has no binding: " + field.Name)
		}
	}
	return nil
}

func (b *builder) structJSON(n hff.Node, binding Binding, typ hff.Type) (string, error) {
	schema, _ := structSchema(typ)
	fields := map[string]Binding{}
	for _, field := range binding.structure.fields {
		fields[field.path[0]] = field
	}
	var members []string
	for _, field := range schema.Fields() {
		child, exists := fields[field.Name]
		if !exists { // Only optional fields can be unbound after validation.
			continue
		}
		key, err := b.parameter(n, hff.String(field.Name))
		if err != nil {
			return "", err
		}
		var value string
		if child.structure != nil {
			value, err = b.structJSON(n, child, field.Type)
		} else if child.relation != nil {
			value, err = b.relationJSON(n, child)
		} else if child.timestamp {
			return "", b.failure(n, "whole struct materialization with a Timestamp field is not yet supported")
		} else {
			column, quoteErr := quoteColumn(child.column)
			if quoteErr != nil {
				return "", quoteErr
			}
			if child.format != "" {
				restored, _, restoreErr := b.jsonBindingCode(child)
				if restoreErr != nil {
					return "", restoreErr
				}
				value = "COALESCE(" + restored + "::json,'null'::json)"
			} else {
				value, err = b.compositeJSON(n, column, field.Type)
			}
		}
		if err != nil {
			return "", err
		}
		member := "to_json(" + key.code + ")::text || ':' || COALESCE((" + value + ")::text,'null')"
		if child.structure != nil {
			member = "CASE WHEN (" + b.predicateCode(child.structure.present) + ") IS TRUE THEN " + member + " ELSE NULL END"
		}
		if child.relation != nil {
			member = "CASE WHEN (" + b.predicateCode(child.relation.config.present) + ") IS TRUE THEN " + member + " ELSE NULL END"
		}

		members = append(members, member)
	}
	object := "('{}'::json)"
	if len(members) > 0 {
		object = "(('{' || concat_ws(','," + strings.Join(members, ",") + ") || '}')::json)"
	}
	return "(CASE WHEN (" + b.predicateCode(binding.structure.present) + ") IS NOT TRUE OR (" + b.predicateCode(binding.structure.null) + ") IS TRUE THEN NULL::json ELSE " + object + " END)", nil
}

// Parent materialization must retain JSONB float-loss provenance, even though
// its resulting object is JSON. A selected non-float sibling remains usable.
func structLosesFloat(binding Binding, typ hff.Type, path []string) bool {
	schema, _ := structSchema(typ)
	types := map[string]hff.Type{}
	for _, field := range schema.Fields() {
		types[field.Name] = field.Type
	}
	for _, field := range binding.structure.fields {
		if len(path) > 0 && path[0] != field.path[0] {
			continue
		}
		if field.relation != nil && relationLosesFloat(*field.relation) {
			return true
		}
		if field.format == JSONB && field.metadata == nil && jsonHasFloat(types[field.path[0]]) {
			return true
		}
		if field.structure != nil {
			nested := path
			if len(nested) > 0 {
				nested = nested[1:]
			}
			if structLosesFloat(field, types[field.path[0]], nested) {
				return true
			}
		}
	}
	return false
}

// Project an unguarded field chain directly from a flattened mapping. Explicit
// parent state checks preserve missing/null propagation while unrelated fields
// are neither materialized nor translated. Guarded chains retain the general
// lowering, which carries no-value separately from null.
func (b *builder) structPath(n hff.Node) (expression, bool, error) {
	root := n
	var reversed []string
	for root.Kind() == hff.FieldNode {
		name, _ := root.Field()
		reversed = append(reversed, name)
		root, _ = root.Child(0)
	}
	var candidates []struct {
		mapping Binding
		fields  []string
	}
	switch root.Kind() {
	case hff.ParameterNode:
		parameter, ok := root.Binding()
		if !ok {
			return expression{}, false, nil
		}
		mapping, ok := b.physicalParameters[parameter.ID()]
		if !ok || (mapping.structure == nil && !(mapping.format == JSONB && mapping.metadata != nil)) {
			return expression{}, false, nil
		}
		fields := make([]string, len(reversed))
		for i, name := range reversed {
			fields[len(reversed)-1-i] = name
		}
		candidates = append(candidates, struct {
			mapping Binding
			fields  []string
		}{mapping, fields})
	case hff.VariableNode:
		path, ok := n.Path()
		if !ok {
			return expression{}, false, nil
		}
		// A nearer explicit mapping owns the path, even when it is not a
		// flattened struct. Do not bypass it with an ancestor optimization.
		for size := len(path); size > 0; size-- {
			if _, bound := b.bindings[pathKey(path[:size])]; !bound {
				continue
			}
			if mapping, found := b.structBindings[pathKey(path[:size])]; found && size < len(path) {
				candidates = append(candidates, struct {
					mapping Binding
					fields  []string
				}{mapping, path[size:]})
			}
			break
		}
	default:
		return expression{}, false, nil
	}
	for _, candidate := range candidates {
		mapping := candidate.mapping
		fields := candidate.fields
		var states []*structBinding
		for offset, name := range fields {
			if mapping.format == JSONB && mapping.metadata != nil {
				value, err := b.structuredMetadataSubpath(n, mapping, fields[offset:], states)
				return value, true, err
			}
			if mapping.structure == nil {
				return expression{}, false, nil
			}
			states = append(states, mapping.structure)
			child, exists := structFieldBinding(mapping, name)
			if !exists {
				if timestampSchema(n.Type()) {
					return expression{code: "NULL::numeric", present: "TRUE", kind: hff.TimestampType}, true, nil
				}
				if kind, ok := scalarKind(n.Type()); ok {
					code := "NULL"
					if kind != hff.NullType {
						code += "::" + sqlType(kind)
					}
					return expression{code: code, present: "TRUE", kind: kind}, true, nil
				}
				value, err := b.jsonDecode(n, "NULL::json", JSON, n.Type(), "TRUE")
				return value, true, err
			}
			mapping = child
			if offset < len(fields)-1 {
				continue
			}
			var absent []string
			for _, state := range states {
				absent = append(absent, "("+b.predicateCode(state.present)+") IS NOT TRUE", "("+b.predicateCode(state.null)+") IS TRUE")
			}
			condition := strings.Join(absent, " OR ")
			if mapping.timestamp {
				code, err := timestampBindingExpression(mapping)
				if err != nil {
					return expression{}, true, err
				}
				return expression{code: "(CASE WHEN " + condition + " THEN NULL::numeric ELSE " + code + " END)", present: "TRUE", kind: hff.TimestampType}, true, nil
			}
			if mapping.structure == nil && mapping.relation == nil && mapping.format == "" {
				if kind, ok := scalarKind(n.Type()); ok {
					column, err := quoteColumn(mapping.column)
					if err != nil {
						return expression{}, true, err
					}
					return expression{code: "(CASE WHEN " + condition + " THEN NULL ELSE " + column + " END)", present: "TRUE", kind: kind}, true, nil
				}
			}
			format := JSON
			var code string
			var err error
			switch {
			case mapping.structure != nil:
				code, err = b.structJSON(n, mapping, n.Type())
			case mapping.relation != nil:
				code, err = b.relationJSON(n, mapping)
			case mapping.format != "":
				code, format, err = b.jsonBindingCode(mapping)
			default:
				column, quoteErr := quoteColumn(mapping.column)
				if quoteErr != nil {
					return expression{}, true, quoteErr
				}
				code, err = b.compositeJSON(n, column, n.Type())
			}
			if err != nil {
				return expression{}, true, err
			}
			code = "(CASE WHEN " + condition + " THEN NULL::" + string(format) + " ELSE " + code + " END)"
			value, err := b.jsonDecode(n, code, format, n.Type(), "TRUE")
			return value, true, err
		}
	}
	return expression{}, false, nil
}
func structFieldBinding(mapping Binding, name string) (Binding, bool) {
	if mapping.structure == nil {
		return Binding{}, false
	}
	for _, field := range mapping.structure.fields {
		if field.path[0] == name {
			return field, true
		}
	}
	return Binding{}, false
}
