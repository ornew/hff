package sql

import "github.com/ornew/hff"

// Preserve the selected value, rather than reconstructing it from an
// aggregate key. Int/Float and signed-zero ties retain the first source row.
func (b *builder) jsonExtreme(n hff.Node, minimum bool) (expression, error) {
	receiver, _ := n.Child(0)
	value, format, keys, err := b.jsonCollectionReceiver(n, true)
	if err != nil {
		return expression{}, err
	}
	if value.kind == hff.NullType {
		return value, nil
	}
	stored := sortListType(b.jsonStoredType(receiver))
	if stored.Kind() != hff.ListType {
		return expression{}, b.failure(n, "numeric extreme requires a recoverable list schema")
	}
	var from, ordinal, term, present string
	family := staticSumFamily(stored.Element())
	if n.ChildCount() == 1 {
		if family == hff.InvalidType {
			document, err := b.comparisonDocument(receiver, value.code, format, stored)
			if err != nil {
				return expression{}, err
			}
			raw := b.project(document)
			array := b.project("(CASE WHEN json_typeof(" + raw + ")='array' THEN " + raw + " ELSE NULL::json END)")
			alias := b.nextAlias()
			from := "json_array_elements(" + array + ") WITH ORDINALITY AS " + alias + "(value,ord)"
			return b.jsonDynamicExtreme(n, value, from, alias+".value", alias+".ord", minimum)
		}
		alias := b.nextAlias()
		from = string(format) + "_array_elements(" + value.code + ") WITH ORDINALITY AS " + alias + "(value,ord)"
		ordinal = alias + ".ord"
		term = jsonScalarCast("("+alias+".value #>> '{}'::text[])", family)
		present = "TRUE"
	} else {
		callback, _ := n.Child(1)
		var computed expression
		var projected hff.Type
		from, ordinal, _, _, computed, projected, _, err = b.jsonCallbackRows(n, value, format, callback, stored.Element(), false, keys)
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
			source := "(SELECT " + document + " AS value," + ordinal + " AS ord FROM " + from + " WHERE " + computed.present + ") AS " + rows
			return b.jsonDynamicExtreme(n, value, source, rows+".value", rows+".ord", minimum)
		}
		term, present = computed.code, computed.present
		if computed.kind == hff.NullType {
			term = "NULL::bigint"
		}
	}
	rows := b.nextAlias()
	source := "(SELECT " + term + " AS value," + ordinal + " AS ord FROM " + from + " WHERE " + present + ") AS " + rows
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL ELSE (SELECT " + rows + ".value FROM " + source + " WHERE " + rows + ".value IS NOT NULL ORDER BY " + rows + ".value" + extremeDirection(minimum) + "," + rows + ".ord LIMIT 1) END)"
	return expression{code: code, present: value.present, kind: family}, nil
}

func extremeDirection(minimum bool) string {
	if minimum {
		return " ASC"
	}
	return " DESC"
}

func (b *builder) jsonDynamicExtreme(n hff.Node, value expression, from, item, ordinal string, minimum bool) (expression, error) {
	category := "json_typeof(" + item + ")"
	text := "(" + item + " #>> '{}'::text[])"
	integer := "(" + category + "='number' AND " + text + " !~ '[.eE]')"
	floating := "(" + category + "='number' AND " + text + " ~ '[.eE]')"
	decoded := b.nextAlias()
	source := from + " CROSS JOIN LATERAL (SELECT CASE WHEN " + floating + " THEN " + jsonFloatCast(text) + " END AS value OFFSET 0) AS " + decoded
	key := "(CASE WHEN " + integer + " THEN " + text + "::numeric WHEN " + floating + " THEN " + numericOrderKey(decoded+".value") + " END)"
	rows := b.nextAlias()
	cte := "WITH " + rows + " AS MATERIALIZED (SELECT " + item + " AS value," + ordinal + " AS ord," + key + " AS key," + category + " NOT IN ('number','null') AS invalid FROM " + source + ")"
	selected := "(SELECT value FROM " + rows + " WHERE key IS NOT NULL ORDER BY key" + extremeDirection(minimum) + ",ord LIMIT 1)"
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::json ELSE (" + cte + " SELECT CASE WHEN EXISTS(SELECT 1 FROM " + rows + " WHERE invalid) THEN 'null'::json ELSE " + selected + " END) END)"
	return b.jsonDecode(n, code, JSON, n.Type(), value.present)
}

// A round-trip decimal preserves Float order, but large integer-valued Floats
// need their exact integer value to compare with adjacent Ints. Below 2^52,
// every integer is exactly representable, so a Float's round-trip interval
// cannot cross an integer. PostgreSQL's shortest Float text also preserves
// integral values there exactly. At positive 2^63, use the exact endpoint:
// its round-trip interval can overlap MaxInt64. More distant out-of-int64
// Floats have intervals wholly outside the Int range.
// This ordering key avoids binary decomposition without rounding an Int.
func numericOrderKey(value string) string {
	return "(CASE WHEN " + value + ">='-9223372036854775808'::double precision AND " + value + "<'9223372036854775808'::double precision AND abs(" + value + ")>=4503599627370496::double precision THEN " + value + "::bigint::numeric WHEN " + value + "='9223372036854775808'::double precision THEN '9223372036854775808'::numeric ELSE " + value + "::text::numeric END)"
}
