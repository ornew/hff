package sql

import "github.com/ornew/hff"

// parsedJSON checks the complete document before exposing it to later
// operations. PostgreSQL syntax validation also decodes Unicode and checks
// duplicate keys; numeric range is an additional HFF contract.
func (b *builder) parsedJSON(text string) string {
	key := "parsed-json:" + text
	if cached, ok := b.sharedPaths[key]; ok {
		return cached.code
	}
	input := b.project(text)
	safe := b.project("(CASE WHEN " + input + " IS JSON WITH UNIQUE KEYS THEN " + input + " ELSE NULL::text END)")
	raw := b.project("(" + safe + "::json)")
	walk, children, arrays, objects := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	v := walk + ".v"
	number := "(" + v + " #>> '{}'::text[])"
	validNumber := validParsedNumber(number)
	arrayChildren := "SELECT " + arrays + ".v FROM json_array_elements(CASE WHEN json_typeof(" + v + ")='array' THEN " + v + " ELSE '[]'::json END) AS " + arrays + "(v)"
	objectChildren := "SELECT " + objects + ".value AS v FROM json_each(CASE WHEN json_typeof(" + v + ")='object' THEN " + v + " ELSE '{}'::json END) AS " + objects
	valid := "(WITH RECURSIVE " + walk + "(v) AS (SELECT " + raw + " UNION ALL SELECT " + children + ".v FROM " + walk + " CROSS JOIN LATERAL (" + arrayChildren + " UNION ALL " + objectChildren + ") AS " + children + " WHERE json_typeof(" + v + ") IN ('array','object')) SELECT NOT EXISTS(SELECT 1 FROM " + walk + " WHERE NOT (CASE WHEN json_typeof(" + v + ")='number' THEN " + validNumber + " ELSE TRUE END)" + "))"
	class := "json_typeof(" + raw + ")"
	checked := "(CASE WHEN " + raw + " IS NULL THEN NULL::json WHEN " + class + " IN ('number','array','object') THEN CASE WHEN " + valid + " THEN " + raw + " ELSE NULL::json END ELSE " + raw + " END)"
	// Scope resets for field-path sharing also isolate parser results. Cache only
	// validated JSON, keeping each call's no-value flag outside this cache.
	projected := b.project(checked)
	if b.sharedPaths == nil {
		b.sharedPaths = make(map[string]expression)
	}
	b.sharedPaths[key] = expression{projected, "TRUE", hff.AnyType}
	return projected
}

func (b *builder) jsonParseCall(n hff.Node, receiver expression) (expression, error) {
	if receiver.kind == hff.NullType {
		return b.jsonDecode(n, "NULL::json", JSON, n.Type(), receiver.present)
	}
	text, err := b.stringConversionInput(n, receiver)
	if err != nil {
		return expression{}, err
	}
	return b.jsonDecodeProjected(n, b.parsedJSON(text), JSON, n.Type(), receiver.present)
}

func validParsedNumber(text string) string {
	return "(CASE WHEN " + text + " ~ '[.eE]' THEN " + parseJSONFloat(text) + " IS NOT NULL ELSE pg_input_is_valid(" + text + ",'bigint') END)"
}
