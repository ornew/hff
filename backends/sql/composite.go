package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// BindComposite maps a struct to a native PostgreSQL composite column. SQL
// attribute names match schema field names. Composite fields are always present;
// a null attribute is an explicit null, not an absent optional field. The caller
// must store values satisfying the schema and the supported physical types,
// including one-dimensional arrays for list attributes. Build does not inspect
// the database. Array lower bounds do not affect HFF's zero-based indexing.
func BindComposite(path hff.Path, column ColumnRef) Binding {
	result := Bind(path, column)
	result.composite = true
	return result
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
