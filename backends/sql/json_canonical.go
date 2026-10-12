package sql

// canonicalJSON serializes a normalized JSON-native value at arbitrary runtime
// depth. Lexicographic bigint paths order opening tokens, child subtrees, and
// closing tokens without changing array order or relying on object storage
// order. Child prefixes carry commas and object keys. Scalar rows emit their
// own tokens but do not expand children.
func (b *builder) canonicalJSON(raw string) string {
	walk, child, items, objects, tokens := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	value := walk + ".v"
	category := "json_typeof(" + value + ")"
	text := "(" + value + " #>> '{}'::text[])"
	// Keep the lexical Float decoder inside the number-token branch and
	// project it once before shortest-roundtrip formatting uses its value.
	decoded := b.nextAlias()
	floating := "(SELECT " + b.floatText(decoded+".v") + " FROM LATERAL (SELECT " + jsonFloatCast(text) + " AS v OFFSET 0) AS " + decoded + ")"
	// Integer text has already been normalized according to its stored schema.
	// Casting as bigint also removes noncanonical integer spelling.
	scalar := "CASE " + category + " WHEN 'string' THEN to_json(" + text + ")::text WHEN 'number' THEN CASE WHEN " + text + " ~ '[.eE]' THEN " + floating + " ELSE (" + text + ")::bigint::text END WHEN 'boolean' THEN (" + text + ")::boolean::text ELSE 'null' END"
	opening := "CASE " + category + " WHEN 'array' THEN '[' WHEN 'object' THEN '{' ELSE " + scalar + " END"
	arrays := "SELECT " + items + ".ord AS idx," + items + ".v,NULL::text AS key FROM json_array_elements(CASE WHEN " + category + "='array' THEN " + value + " ELSE '[]'::json END) WITH ORDINALITY AS " + items + "(v,ord)"
	maps := "SELECT row_number() OVER (ORDER BY " + objects + ".key COLLATE \"C\") AS idx," + objects + ".value AS v," + objects + ".key FROM json_each(CASE WHEN " + category + "='object' THEN " + value + " ELSE '{}'::json END) AS " + objects
	prefix := "(CASE WHEN " + child + ".idx>1 THEN ',' ELSE '' END)||COALESCE(to_json(" + child + ".key)::text||':','')"
	return "(WITH RECURSIVE " + walk + "(path,v,prefix) AS (SELECT ARRAY[]::bigint[],COALESCE(" + raw + ",'null'::json),''::text UNION ALL SELECT " + walk + ".path||" + child + ".idx," + child + ".v," + prefix + " FROM " + walk + " CROSS JOIN LATERAL (" + arrays + " UNION ALL " + maps + ") AS " + child + " WHERE " + category + " IN ('array','object')), " + tokens + " AS (SELECT path||0::bigint AS path,prefix||" + opening + " AS token FROM " + walk + " UNION ALL SELECT path||9223372036854775807::bigint,CASE " + category + " WHEN 'array' THEN ']' ELSE '}' END FROM " + walk + " WHERE " + category + " IN ('array','object')) SELECT string_agg(token,'' ORDER BY path) FROM " + tokens + ")"
}
