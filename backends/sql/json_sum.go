package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Sum one statically known numeric family through PostgreSQL numeric so
// intermediate totals do not overflow the result carrier. Dynamic numeric
// projections need a separate path that preserves each contributing subtype.
func (b *builder) jsonSum(n hff.Node) (expression, error) {
	receiver, _ := n.Child(0)
	value, err := b.expression(receiver)
	if err != nil {
		return expression{}, err
	}
	if value.kind == hff.NullType {
		return value, nil
	}
	value, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return expression{}, err
	}
	stored := sortListType(b.jsonStoredType(receiver))
	if stored.Kind() != hff.ListType {
		return expression{}, b.failure(n, "sum requires a recoverable list schema")
	}
	itemType := stored.Element()
	var from, term, present, ordinal string
	var family hff.TypeKind
	if n.ChildCount() == 1 {
		family = staticSumFamily(itemType)
		if family == hff.InvalidType {
			return b.jsonDynamicSum(n, receiver, value, format, stored)
		}
		alias := b.nextAlias()
		from = string(format) + "_array_elements(" + value.code + ") AS " + alias + "(value)"
		term = jsonScalarCast("("+alias+".value #>> '{}'::text[])", family)
		present = "TRUE"
	} else {
		callback, _ := n.Child(1)
		var computed expression
		var projected hff.Type
		from, ordinal, _, _, computed, projected, err = b.jsonCallbackRows(n, value, format, callback, itemType, false)
		if err != nil {
			return expression{}, err
		}
		family = staticSumFamily(projected)
		if family == hff.InvalidType || computed.kind != family && computed.kind != hff.NullType {
			body, _ := callback.Child(0)
			document, err := b.computedJSONDocument(body, computed, projected)
			if err != nil {
				return expression{}, err
			}
			rows := b.nextAlias()
			source := "(SELECT " + document + " AS value FROM " + from + " WHERE " + computed.present + ") AS " + rows
			return b.jsonDynamicSumRows(n, value, source, rows+".value")
		}
		term, present = computed.code, computed.present
		if computed.kind == hff.NullType {
			term = "NULL::bigint"
		}
		// A captured outer SQL column alone would make PostgreSQL assign SUM
		// to the outer query. Keep the local array row in the aggregate input.
		present = both(present, ordinal+">0")
	}
	if strings.TrimSpace(term) == "" {
		return expression{}, b.failure(n, "sum has no numeric projection")
	}
	// SUM(numeric) avoids intermediate bigint or Float overflow. PostgreSQL's
	// direct float-to-numeric cast discards significant digits; the required
	// round-trip Float text retains them before numeric accumulation.
	numericTerm := "(" + term + ")::numeric"
	if family == hff.FloatType {
		numericTerm = "(" + term + ")::text::numeric"
	}
	aggregate := "sum(CASE WHEN " + present + " THEN " + numericTerm + " END)"
	alias := b.nextAlias()
	if family == hff.FloatType {
		floatValue := alias + ".total::double precision"
		result := "(CASE WHEN " + alias + ".total IS NULL THEN '0'::json WHEN pg_input_is_valid(" + alias + ".total::text,'double precision') THEN " + b.floatText(floatValue) + "::json ELSE 'null'::json END)"
		code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::json ELSE (SELECT " + result + " FROM (SELECT " + aggregate + " AS total FROM " + from + ") AS " + alias + ") END)"
		return b.jsonDecode(n, code, JSON, n.Type(), value.present)
	}
	total := "COALESCE(" + aggregate + ",0::numeric)"
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::bigint ELSE (SELECT CASE WHEN " + alias + ".total BETWEEN '-9223372036854775808'::numeric AND '9223372036854775807'::numeric THEN " + alias + ".total::bigint ELSE NULL::bigint END FROM (SELECT " + total + " AS total FROM " + from + ") AS " + alias + ") END)"
	return expression{code: code, present: value.present, kind: hff.IntType}, nil
}

// A recoverable mixed list can choose its result family from the actual
// contributing rows. JSONB must restore numeric subtype metadata first.
func (b *builder) jsonDynamicSum(n, receiver hff.Node, value expression, format JSONFormat, stored hff.Type) (expression, error) {
	document, err := b.comparisonDocument(receiver, value.code, format, stored)
	if err != nil {
		return expression{}, err
	}
	raw := b.project(document)
	array := b.project("(CASE WHEN json_typeof(" + raw + ")='array' THEN " + raw + " ELSE NULL::json END)")
	alias := b.nextAlias()
	source := "json_array_elements(" + array + ") AS " + alias + "(value)"
	return b.jsonDynamicSumRows(n, value, source, alias+".value")
}

func (b *builder) jsonDynamicSumRows(n hff.Node, value expression, from, item string) (expression, error) {
	category := "json_typeof(" + item + ")"
	text := "(" + item + " #>> '{}'::text[])"
	integer := "(" + category + "='number' AND " + text + " !~ '[.eE]')"
	floating := "(" + category + "='number' AND " + text + " ~ '[.eE]')"
	floatValue := jsonFloatCast(text)
	term := "(CASE WHEN " + integer + " THEN " + text + "::numeric WHEN " + floating + " THEN (" + floatValue + ")::text::numeric END)"
	validFloat := "(" + floating + " AND " + floatValue + " IS NOT NULL)"
	result := b.nextAlias()
	total := result + ".total"
	floatResult := b.floatText(total+"::double precision") + "::json"
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::json ELSE (SELECT CASE WHEN " + result + ".invalid THEN 'null'::json WHEN " + total + " IS NULL THEN '0'::json WHEN " + result + ".floating THEN CASE WHEN pg_input_is_valid(" + total + "::text,'double precision') THEN " + floatResult + " ELSE 'null'::json END WHEN " + total + " BETWEEN '-9223372036854775808'::numeric AND '9223372036854775807'::numeric THEN to_json(" + total + "::bigint) ELSE 'null'::json END FROM (SELECT sum(" + term + ") AS total,COALESCE(bool_or(" + validFloat + "),FALSE) AS floating,COALESCE(bool_or(" + category + " NOT IN ('number','null')),FALSE) AS invalid FROM " + from + ") AS " + result + ") END)"
	return b.jsonDecode(n, code, JSON, n.Type(), value.present)
}

func staticSumFamily(typ hff.Type) hff.TypeKind {
	switch typ.Kind() {
	case hff.IntType, hff.NullType, hff.NeverType:
		return hff.IntType
	case hff.FloatType:
		return hff.FloatType
	case hff.UnionType:
		family := hff.InvalidType
		for _, member := range typ.Members() {
			if member.Kind() == hff.NullType || member.Kind() == hff.NeverType {
				continue
			}
			memberFamily := staticSumFamily(member)
			if memberFamily == hff.InvalidType || family != hff.InvalidType && family != memberFamily {
				return hff.InvalidType
			}
			family = memberFamily
		}
		if family == hff.InvalidType {
			return hff.IntType
		}
		return family
	}
	return hff.InvalidType
}
