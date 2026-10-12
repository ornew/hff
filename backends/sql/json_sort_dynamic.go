package sql

import "github.com/ornew/hff"

// A dynamic sort consumes only the receiver's list branches. Other union
// branches must not create representation requirements for an unused value.
func sortListType(typ hff.Type) hff.Type {
	if typ.Kind() == hff.AnyType {
		return hff.NewListType(typ)
	}
	if typ.Kind() == hff.ListType {
		return typ
	}
	var elements []hff.Type
	if typ.Kind() == hff.UnionType {
		for _, member := range typ.Members() {
			list := sortListType(member)
			if list.Kind() == hff.ListType {
				elements = append(elements, list.Element())
			}
		}
	}
	if len(elements) == 0 {
		return hff.Type{}
	}
	return hff.NewListType(hff.NewUnionType(elements...))
}

func (b *builder) jsonSort(n hff.Node, value expression) (expression, error) {
	if value.kind == hff.NullType {
		return value, nil
	}
	receiver, _ := n.Child(0)
	stored := sortListType(b.jsonStoredType(receiver))
	if value.kind == hff.ListType && stored.Kind() == hff.ListType && sortListType(receiver.Type()).Key() == stored.Key() {
		// Keep existing SQL for homogeneous, schema-resolved lists. Ambiguous
		// numeric unions and Any need a single exact common numeric key.
		if _, err := b.jsonEqualityMembers(n, stored.Element()); err == nil {
			format, ok := b.jsonValues[value.code]
			if !ok {
				return expression{}, b.failure(n, "sort requires a recoverable JSON list")
			}
			return b.jsonSequence(n, "sort", []expression{value}, format)
		}
	}
	list, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return expression{}, err
	}
	if stored.Kind() != hff.ListType {
		return expression{}, b.failure(n, "sort requires an applicable list schema")
	}
	document, err := b.comparisonDocument(receiver, list.code, format, stored)
	if err != nil {
		return expression{}, err
	}
	normalized := b.project(document)
	// Normalization returns JSON null for ordinary null; array expansion must
	// receive SQL NULL instead, including for a wrong dynamic receiver family.
	raw := b.project("(CASE WHEN json_typeof(" + normalized + ")='array' THEN " + normalized + " ELSE NULL::json END)")
	alias := b.nextAlias()
	item := alias + ".value"
	category := "json_typeof(" + item + ")"
	text := "(" + item + " #>> '{}'::text[])"
	integer := "(" + category + "='number' AND " + text + " !~ '[.eE]')"
	floating := "(" + category + "='number' AND " + text + " ~ '[.eE]')"
	i := "(CASE WHEN " + integer + " THEN " + jsonScalarCast(text, hff.IntType) + "::numeric END)"
	f := "(CASE WHEN " + floating + " THEN " + jsonScalarCast(text, hff.FloatType) + " END)"
	number := "(CASE WHEN " + integer + " THEN " + i + " WHEN " + floating + " THEN " + b.floatNumeric(f) + " END)"
	stringKey := "(CASE WHEN " + category + "='string' THEN " + text + " END) COLLATE \"C\""
	from := "json_array_elements(" + raw + ") WITH ORDINALITY AS " + alias + "(value,ord)"
	invalid := "EXISTS(SELECT 1 FROM " + from + " WHERE " + category + " NOT IN ('number','string'))"
	mixed := "(SELECT count(DISTINCT " + category + ")>1 FROM " + from + ")"
	// Int/Float and signed-zero ties keep their normalized original spelling,
	// so stable ordering is observable even though their numeric keys coincide.
	aggregate := "(SELECT COALESCE(json_agg(" + item + " ORDER BY " + number + "," + stringKey + "," + alias + ".ord),'[]'::json) FROM " + from + ")"
	code := "(CASE WHEN " + raw + " IS NULL OR " + invalid + " OR " + mixed + " THEN NULL::json ELSE " + aggregate + " END)"
	b.jsonValues[code] = JSON
	return expression{code, value.present, hff.ListType}, nil
}
