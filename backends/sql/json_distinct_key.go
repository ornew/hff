package sql

import "github.com/ornew/hff"

// These keys represent JSON-native equality only. Tagged string families keep
// the schema-aware pairwise path: spelling alone cannot identify their type.
func structuralDistinctKeyType(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.AnyType, hff.NeverType, hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType:
		return true
	case hff.ListType, hff.MapType:
		return structuralDistinctKeyType(typ.Element())
	case hff.StructType:
		for _, field := range typ.Fields() {
			if !structuralDistinctKeyType(field.Type) {
				return false
			}
		}
		return true
	case hff.UnionType:
		for _, member := range typ.Members() {
			if !structuralDistinctKeyType(member) {
				return false
			}
		}
		return true
	}
	return false
}

// A normalized tree with no possible Float can use JSONB equality directly.
// Float spelling needs a value key, including exact ties with int64 values.
func structuralDistinctNeedsNumericKey(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.AnyType, hff.FloatType:
		return true
	case hff.ListType, hff.MapType:
		return structuralDistinctNeedsNumericKey(typ.Element())
	case hff.StructType:
		for _, field := range typ.Fields() {
			if structuralDistinctNeedsNumericKey(field.Type) {
				return true
			}
		}
	case hff.UnionType:
		for _, member := range typ.Members() {
			if structuralDistinctNeedsNumericKey(member) {
				return true
			}
		}
	}
	return false
}

// Walk each normalized item once, rather than walking every prior-row pair.
// The signature includes containers, nulls and full paths. JSON path-array
// encoding avoids collisions between arbitrary object keys; parent categories
// distinguish numeric object keys from list indexes. Keep the original value
// separately so numeric keys never change the first value's representation.
func (b *builder) jsonStructuralDistinctKey(item, ordinal string) string {
	walk, child, decoded := b.nextAlias(), b.nextAlias(), b.nextAlias()
	value := walk + ".value"
	category := "json_typeof(" + value + ")"
	text := "(" + value + " #>> '{}'::text[])"
	floating := "(" + category + "='number' AND " + text + " ~ '[.eE]')"
	f := decoded + ".value"
	children := "SELECT a.value,(a.ord-1)::text AS key FROM json_array_elements(CASE WHEN " + category + "='array' THEN " + value + " ELSE '[]'::json END) WITH ORDINALITY AS a(value,ord) UNION ALL SELECT o.value,o.key FROM json_each(CASE WHEN " + category + "='object' THEN " + value + " ELSE '{}'::json END) AS o(key,value)"
	cte := walk + "(value,path) AS (SELECT " + item + ",ARRAY[]::text[] UNION ALL SELECT " + child + ".value," + walk + ".path||" + child + ".key FROM " + walk + " CROSS JOIN LATERAL (" + children + ") AS " + child + ")"
	number := "CASE WHEN " + floating + " THEN " + numericOrderKey(f) + " ELSE " + text + "::numeric END"
	scalar := "CASE WHEN " + category + "='number' THEN to_jsonb(" + number + ") WHEN " + category + " IN ('string','boolean') THEN to_jsonb(" + text + ") ELSE 'null'::jsonb END"
	key := "jsonb_object_agg(to_json(" + walk + ".path)::text,jsonb_build_array(" + category + "," + scalar + "))"
	// Rejected Float text can survive an Any boundary. Its numeric equality is
	// false even against itself; assigning its row ordinal prevents deduplication.
	valid := "bool_and(NOT " + floating + " OR " + f + " IS NOT NULL)"
	from := walk + " CROSS JOIN LATERAL (SELECT CASE WHEN " + floating + " THEN " + jsonFloatCast(text) + " END AS value OFFSET 0) AS " + decoded
	return "(WITH RECURSIVE " + cte + " SELECT jsonb_build_array(" + key + ",CASE WHEN " + valid + " THEN 0::bigint ELSE " + ordinal + " END) FROM " + from + ")"
}
