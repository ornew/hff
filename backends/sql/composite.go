package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// BindComposite maps a struct path to one native PostgreSQL composite column.
// SQL attribute names must match HFF schema field names. Composite attributes
// are always present: an SQL NULL attribute is an explicit HFF null, not an
// absent optional field. The caller must store schema-compatible data using
// supported physical types, including one-dimensional SQL arrays for list
// fields. Build does not inspect the database or verify actual array rank.
// PostgreSQL array lower bounds do not change HFF's zero-based indexing.
func BindComposite(path hff.Path, column ColumnRef) Binding {
	result := Bind(path, column)
	result.composite = true
	return result
}

// Read a plain attribute path before converting its selected value to JSON.
// PostgreSQL attribute access propagates a null parent without conflating it
// with an existing all-null row. Guards and other value expressions continue
// through the general lowering so their separate no-value state is preserved.
func (b *builder) compositePath(n hff.Node) (expression, bool, error) {
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
	for size := len(path) - 1; size > 0; size-- {
		key := pathKey(path[:size])
		if !b.compositeBindings[key] {
			continue
		}
		column := b.bindings[key]
		for _, field := range path[size:] {
			identifier, err := quoteColumn(Column(field))
			if err != nil {
				return expression{}, true, err
			}
			column = "(" + column + ")." + identifier
		}
		code, err := b.compositeJSON(n, column, n.Type())
		if err != nil {
			return expression{}, true, err
		}
		value, err := b.jsonDecode(n, code, b.jsonBindings[key], n.Type(), "TRUE")
		return value, true, err
	}
	return expression{}, false, nil
}

func (b *builder) compositeJSON(n hff.Node, column string, typ hff.Type) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	kind, ok := jsonKind(typ)
	if !ok {
		return "", b.failure(n, "composite field requires an unambiguous physical type")
	}
	switch kind {
	case hff.NullType:
		return "'null'::json", nil
	case hff.BoolType, hff.IntType, hff.StringType:
		return "COALESCE(to_json(" + column + "),'null'::json)", nil
	case hff.FloatType:
		return b.floatText(column) + "::json", nil
	case hff.StructType:
		var fields []string
		for _, field := range nonnullJSONType(typ).Fields() {
			identifier, err := quoteColumn(Column(field.Name))
			if err != nil {
				return "", err
			}
			key, err := b.parameter(n, hff.String(field.Name))
			if err != nil {
				return "", err
			}
			value, err := b.compositeJSON(n, "("+column+")."+identifier, field.Type)
			if err != nil {
				return "", err
			}
			fields = append(fields, key.code, value)
		}
		// IS NULL on a PostgreSQL row also matches a non-null row whose fields
		// are all null. IS NOT DISTINCT FROM tests only the row's own identity.
		return "(CASE WHEN " + column + " IS NOT DISTINCT FROM NULL THEN NULL::json ELSE json_build_object(" + strings.Join(fields, ",") + ") END)", nil
	case hff.ListType:
		element := nonnullJSONType(typ).Element()
		k, ok := jsonKind(element)
		if !ok || k == hff.ListType || k == hff.MapType {
			return "", b.failure(n, "composite list fields require one-dimensional SQL arrays of supported elements")
		}
		alias := b.nextAlias()
		// Index the array directly: unnest expands composite elements into
		// attributes, losing the distinction between null and all-null rows.
		item, err := b.compositeJSON(n, "("+column+")["+alias+".index]", element)
		if err != nil {
			return "", err
		}
		return "(CASE WHEN " + column + " IS NULL THEN NULL::json ELSE (SELECT COALESCE(json_agg(" + item + " ORDER BY " + alias + ".index),'[]'::json) FROM generate_subscripts(" + column + ",1) AS " + alias + "(index)) END)", nil
	}
	return "", b.failure(n, "composite field has an unsupported physical representation")
}
