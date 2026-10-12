package sql

import (
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
)

// SQLPredicate is a caller-owned, trusted Boolean SQL expression. It is not
// parsed from HFF and must never interpolate untrusted query text or values.
// References name identifiers that must be reserved against generated aliases.
type SQLPredicate struct {
	code       string
	references []ColumnRef
}

func TrustedSQL(code string, references ...ColumnRef) SQLPredicate {
	return SQLPredicate{code: code, references: append([]ColumnRef(nil), references...)}
}

type structBinding struct {
	present, null SQLPredicate
	fields        []Binding
}

// BindStruct maps a struct to separate columns with explicit parent state.
// Field binding paths are one literal name relative to this struct. Nested
// BindStruct calls provide independent parent states for nested structures.
// Optional unbound fields are absent; required fields must have bindings.
// A scalar SQL NULL is an explicit null, never an omitted field.
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
		} else {
			column, quoteErr := quoteColumn(child.column)
			if quoteErr != nil {
				return "", quoteErr
			}
			if child.format != "" {
				value = "COALESCE(" + column + "::json,'null'::json)"
			} else {
				value, err = b.compositeJSON(n, column, field.Type)
			}
		}
		if err != nil {
			return "", err
		}
		member := "to_json(" + key.code + ")::text || ':' || COALESCE((" + value + ")::text,'null')"
		if child.structure != nil {
			member = "CASE WHEN (" + child.structure.present.code + ") IS TRUE THEN " + member + " ELSE NULL END"
		}
		if child.relation != nil {
			member = "CASE WHEN (" + child.relation.config.present.code + ") IS TRUE THEN " + member + " ELSE NULL END"
		}

		members = append(members, member)
	}
	object := "('{}'::json)"
	if len(members) > 0 {
		object = "(('{' || concat_ws(','," + strings.Join(members, ",") + ") || '}')::json)"
	}
	return "(CASE WHEN (" + binding.structure.present.code + ") IS NOT TRUE OR (" + binding.structure.null.code + ") IS TRUE THEN NULL::json ELSE " + object + " END)", nil
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
		if field.format == JSONB && jsonHasFloat(types[field.path[0]]) {
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
