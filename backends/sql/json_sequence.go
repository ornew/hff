package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) jsonSequence(n hff.Node, name string, values []expression, format JSONFormat) (expression, error) {
	r := values[0]
	if r.kind != hff.ListType {
		return expression{}, b.failure(n, "sort and list concat require a list receiver")
	}
	if name == "concat" {
		return b.jsonConcat(n, values, format)
	}
	child, _ := n.Child(0)
	lists, err := b.jsonEqualityMembers(n, child.Type())
	if err != nil || len(lists) != 1 {
		if err == nil {
			err = b.failure(n, "sort requires an unambiguous list schema")
		}
		return expression{}, err
	}
	members, err := b.jsonEqualityMembers(n, lists[0].Element())
	if err != nil {
		return expression{}, err
	}
	alias := b.nextAlias()
	raw := alias + ".value"
	category := string(format) + "_typeof(" + raw + ")"
	var valid, order []string
	for _, t := range members {
		switch t.Kind() {
		case hff.IntType, hff.FloatType, hff.StringType:
			condition := category + "='" + jsonCategory(t.Kind()) + "'"
			valid = append(valid, condition)
			key := "(CASE WHEN " + condition + " THEN " + jsonScalarCast("("+raw+" #>> '{}'::text[])", t.Kind()) + " END)"
			if t.Kind() == hff.StringType {
				key += " COLLATE \"C\""
			}
			order = append(order, key)
		}
	}
	allowed := "FALSE"
	if len(valid) > 0 {
		allowed = "(" + strings.Join(valid, " OR ") + ")"
	}
	from := string(format) + "_array_elements(" + r.code + ") WITH ORDINALITY AS " + alias + "(value,ord)"
	// Null elements and incompatible domains are data failures. Empty arrays
	// remain valid even when their schema has no orderable element member.
	invalid := "EXISTS(SELECT 1 FROM " + from + " WHERE NOT COALESCE(" + allowed + ",FALSE))"
	mixed := "(SELECT count(DISTINCT " + category + ")>1 FROM " + from + ")"
	// Ordinality is the final key, preserving the original order of equal
	// elements independently of the database's aggregate/sort implementation.
	if !canonicalScalarSort(n) && !b.orderBlind[n] {
		order = append(order, alias+".ord")
	}
	aggregateOrder := ""
	if len(order) > 0 {
		aggregateOrder = " ORDER BY " + strings.Join(order, ",")
	}
	aggregate := "(SELECT COALESCE(" + string(format) + "_agg(" + raw + aggregateOrder + "),'[]'::" + string(format) + ") FROM " + from + ")"
	code := "(CASE WHEN " + r.code + " IS NULL OR " + invalid + " OR " + mixed + " THEN NULL ELSE " + aggregate + " END)"
	b.jsonValues[code] = format
	return expression{code, r.present, hff.ListType}, nil
}

func (b *builder) jsonConcat(n hff.Node, values []expression, format JSONFormat) (expression, error) {
	converted := append([]expression(nil), values...)
	for i, value := range values {
		if value.kind == hff.NullType {
			continue
		}
		child, _ := n.Child(i)
		physical, ok := b.jsonValues[value.code]
		if !ok || value.kind != hff.ListType {
			return expression{}, b.failure(n, "list concat requires JSON list operands")
		}
		document, err := b.comparisonDocument(child, value.code, physical, child.Type())
		if err != nil {
			return expression{}, err
		}
		code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::json ELSE " + document + " END)"
		b.jsonValues[code] = JSON
		converted[i] = expression{code, value.present, hff.ListType}
	}
	return b.concatJSONLists(n, converted, JSON)
}

// Operands have already been normalized to preserve their declared subtypes.
func (b *builder) concatJSONLists(n hff.Node, values []expression, format JSONFormat) (expression, error) {
	r, arg := values[0], values[1]
	if arg.kind == hff.NullType {
		code := "NULL::json"
		b.jsonValues[code] = JSON
		return expression{code, ordinaryPresence(values), hff.ListType}, nil
	}
	rf, ok := b.jsonValues[arg.code]
	if arg.kind != hff.ListType || !ok {
		return expression{}, b.failure(n, "list concat requires a JSON list argument")
	}
	left, right, result := b.nextAlias(), b.nextAlias(), b.nextAlias()
	// Converting a jsonb element to json changes its physical format only;
	// its HFF scalar subtype remains determined by the list's schema.
	rows := "SELECT 0 AS segment," + left + ".ord," + left + ".value::json AS value FROM " + string(format) + "_array_elements(" + r.code + ") WITH ORDINALITY AS " + left + "(value,ord) UNION ALL SELECT 1," + right + ".ord," + right + ".value::json FROM " + string(rf) + "_array_elements(" + arg.code + ") WITH ORDINALITY AS " + right + "(value,ord)"
	aggregateOrder := " ORDER BY " + result + ".segment," + result + ".ord"
	if b.orderBlind[n] {
		aggregateOrder = ""
	}
	code := "(CASE WHEN " + r.code + " IS NULL OR " + arg.code + " IS NULL THEN NULL ELSE (SELECT COALESCE(json_agg(" + result + ".value" + aggregateOrder + "),'[]'::json) FROM (" + rows + ") AS " + result + ") END)"
	b.jsonValues[code] = JSON
	return expression{code, ordinaryPresence(values), hff.ListType}, nil
}
