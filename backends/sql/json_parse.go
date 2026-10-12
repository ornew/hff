package sql

import "github.com/ornew/hff"

// parsedJSON checks the complete document before exposing it to later
// operations. PostgreSQL syntax validation also decodes Unicode and checks
// duplicate keys; numeric range is an additional HFF contract.
func (b *builder) parsedJSON(text string) string {
	input := b.project(text)
	safe := b.project("(CASE WHEN " + input + " IS JSON WITH UNIQUE KEYS THEN " + input + " ELSE NULL::text END)")
	raw := b.project("(" + safe + "::json)")
	walk, children, arrays, objects := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	v := walk + ".v"
	number := "(" + v + " #>> '{}'::text[])"
	validNumber := "(CASE WHEN " + number + " ~ '[.eE]' THEN " + parseJSONFloat(number) + " IS NOT NULL ELSE pg_input_is_valid(" + number + ",'bigint') END)"
	arrayChildren := "SELECT " + arrays + ".v FROM json_array_elements(CASE WHEN json_typeof(" + v + ")='array' THEN " + v + " ELSE '[]'::json END) AS " + arrays + "(v)"
	objectChildren := "SELECT " + objects + ".value AS v FROM json_each(CASE WHEN json_typeof(" + v + ")='object' THEN " + v + " ELSE '{}'::json END) AS " + objects
	valid := "(WITH RECURSIVE " + walk + "(v) AS (SELECT " + raw + " UNION ALL SELECT " + children + ".v FROM " + walk + " CROSS JOIN LATERAL (" + arrayChildren + " UNION ALL " + objectChildren + ") AS " + children + ") SELECT NOT EXISTS(SELECT 1 FROM " + walk + " WHERE NOT (CASE WHEN json_typeof(" + v + ")='number' THEN " + validNumber + " ELSE TRUE END)" + "))"
	return "(CASE WHEN " + valid + " THEN " + raw + " ELSE NULL::json END)"
}

func (b *builder) jsonParseCall(n hff.Node, receiver expression) (expression, error) {
	text := receiver.code
	switch receiver.kind {
	case hff.NullType:
		return b.jsonDecode(n, "NULL::json", JSON, n.Type(), receiver.present)
	case hff.StringType:
	case hff.AnyType:
		format, ok := b.jsonValues[receiver.code]
		if !ok {
			return expression{}, b.failure(n, "JSON parsing requires a recoverable string receiver")
		}
		text = "(CASE WHEN " + string(format) + "_typeof(" + receiver.code + ")='string' THEN (" + receiver.code + " #>> '{}'::text[]) ELSE NULL::text END)"
	default:
		// Partial union dispatch returns null on inapplicable runtime families.
		return b.jsonDecode(n, "NULL::json", JSON, n.Type(), receiver.present)
	}
	return b.jsonDecode(n, b.parsedJSON(text), JSON, n.Type(), receiver.present)
}
