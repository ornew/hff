package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) jsonSelectionReceiver(n hff.Node) (expression, hff.Type, error) {
	receiver, _ := n.Child(0)
	value, err := b.expression(receiver)
	if err != nil || value.kind == hff.NullType {
		return value, hff.Type{}, err
	}
	value, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return expression{}, hff.Type{}, err
	}
	stored := sortListType(b.jsonStoredType(receiver))
	if stored.Kind() != hff.ListType {
		return expression{}, hff.Type{}, b.failure(n, "list selection requires a recoverable list schema")
	}
	document, err := b.comparisonDocument(receiver, value.code, format, stored)
	if err != nil {
		return expression{}, hff.Type{}, err
	}
	raw := b.project(document)
	array := b.project("(CASE WHEN json_typeof(" + raw + ")='array' THEN " + raw + " ELSE NULL::json END)")
	b.jsonValues[array] = JSON
	b.rememberNormalizedJSON(array, JSON, stored)
	return expression{code: array, present: value.present, kind: hff.ListType}, stored, nil
}

func (b *builder) jsonFind(n hff.Node) (expression, error) {
	value, stored, err := b.jsonSelectionReceiver(n)
	if err != nil || value.kind == hff.NullType {
		return value, err
	}
	callback, _ := n.Child(1)
	from, ordinal, item, predicate, _, _, err := b.jsonCallbackRows(n, value, JSON, callback, stored.Element(), true)
	if err != nil {
		return expression{}, err
	}
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::json ELSE (SELECT " + item + " FROM " + from + " WHERE " + predicate + " ORDER BY " + ordinal + " LIMIT 1) END)"
	retained := hff.NewUnionType(stored.Element(), hff.PrimitiveType(hff.NullType))
	b.rememberNormalizedJSON(code, JSON, retained)
	return b.jsonDecode(n, code, JSON, retained, value.present)
}

func (b *builder) jsonDistinct(n hff.Node) (expression, error) {
	value, stored, err := b.jsonSelectionReceiver(n)
	if err != nil || value.kind == hff.NullType {
		return value, err
	}
	alias, rows := b.nextAlias(), b.nextAlias()
	item, ordinal := alias+".value", alias+".ord"
	from := "json_array_elements(" + value.code + ") WITH ORDINALITY AS " + alias + "(value,ord)"
	var selected, cte string
	if scalarDistinctType(stored.Element()) {
		category := "json_typeof(" + item + ")"
		text := "(" + item + " #>> '{}'::text[])"
		columns, keys := []string{category + " AS category"}, []string{"category"}
		integer, floating := jsonNumericCandidates(stored.Element())
		if integer || floating {
			var key string
			if integer && floating {
				decoded := b.nextAlias()
				f := decoded + ".value"
				from += " CROSS JOIN LATERAL (SELECT CASE WHEN " + category + "='number' AND " + text + " ~ '[.eE]' THEN " + jsonFloatCast(text) + " END AS value OFFSET 0) AS " + decoded
				key = "CASE WHEN " + category + "='number' THEN CASE WHEN " + text + " ~ '[.eE]' THEN " + numericOrderKey(f) + " ELSE " + text + "::numeric END END"
			} else {
				kind := hff.IntType
				if floating {
					kind = hff.FloatType
				}
				key = "CASE WHEN " + category + "='number' THEN " + jsonScalarCast(text, kind) + " END"
			}
			columns = append(columns, key+" AS number")
			keys = append(keys, "number")
		}
		columns = append(columns, "(CASE WHEN "+category+"='string' THEN "+text+" END) COLLATE \"C\" AS text", "CASE WHEN "+category+"='boolean' THEN "+text+"::boolean END AS boolean")
		keys = append(keys, "text", "boolean")
		cte = "WITH " + rows + " AS MATERIALIZED (SELECT " + item + " AS value," + ordinal + " AS ord," + strings.Join(columns, ",") + " FROM " + from + ")"
		key := strings.Join(keys, ",")
		selected = "SELECT DISTINCT ON (" + key + ") value,ord FROM " + rows + " ORDER BY " + key + ",ord"
	} else if structuralDistinctKeyType(stored.Element()) {
		key := item + "::jsonb"
		if structuralDistinctNeedsNumericKey(stored.Element()) {
			key = b.jsonStructuralDistinctKey(item, ordinal)
		}
		cte = "WITH " + rows + " AS MATERIALIZED (SELECT " + item + " AS value," + ordinal + " AS ord," + key + " AS key FROM " + from + ")"
		selected = "SELECT DISTINCT ON (key) value,ord FROM " + rows + " ORDER BY key,ord"
	} else {
		cte = "WITH " + rows + " AS MATERIALIZED (SELECT " + item + " AS value," + ordinal + " AS ord FROM " + from + ")"
		left, right := b.nextAlias(), b.nextAlias()
		receiver, _ := n.Child(0)
		equal, err := b.jsonItemEquality(n, left+".value", JSON, stored.Element(), b.jsonItemsRoles(receiver), right+".value", JSON, stored.Element(), b.jsonItemsRoles(receiver))
		if err != nil {
			return expression{}, err
		}
		selected = "SELECT " + left + ".value," + left + ".ord FROM " + rows + " AS " + left + " WHERE NOT EXISTS(SELECT 1 FROM " + rows + " AS " + right + " WHERE " + right + ".ord<" + left + ".ord AND " + equal + ")"
	}
	picked := b.nextAlias()
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::json ELSE (" + cte + " SELECT COALESCE(json_agg(" + picked + ".value ORDER BY " + picked + ".ord),'[]'::json) FROM (" + selected + ") AS " + picked + ") END)"
	b.jsonValues[code] = JSON
	b.rememberNormalizedJSON(code, JSON, stored)
	return expression{code: code, present: value.present, kind: hff.ListType}, nil
}

func scalarDistinctType(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.NeverType, hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType:
		return true
	case hff.UnionType:
		for _, member := range typ.Members() {
			if !scalarDistinctType(member) {
				return false
			}
		}
		return true
	}
	return false
}
