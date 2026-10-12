package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// JSONBindingOption configures one physical JSON binding. BindJSON applies
// options from left to right; later metadata options replace earlier ones.
// A zero option is invalid and is rejected during Build.
type JSONBindingOption struct{ apply func(*Binding) }

// WithTypeMetadata binds a JSONB column containing version-1 JSON Pointer
// metadata relative to the data document. The caller guarantees its consistency
// with the data and schema, including version 1 and every required type/text
// entry. Metadata currently restores int/float distinctions and numeric text;
// it does not encode bytes, timestamps, or durations. Build rejects unsupported
// configurations but does not inspect rows or validate stored metadata. Use it
// as BindJSON(path, dataColumn, JSONB, WithTypeMetadata(metadataColumn)).
func WithTypeMetadata(column ColumnRef) JSONBindingOption {
	return JSONBindingOption{func(binding *Binding) {
		owned := Column(column.parts...)
		binding.metadata = &owned
	}}
}

// restoreJSONMetadata reconstructs a textual JSON document before ordinary
// typed lowering. Keeping it as json (rather than jsonb) preserves restored
// numeric subtype spelling and signed zero through existing collection paths.
func (b *builder) restoreJSONMetadata(data, metadata string) string {
	walk, child, tokens := b.nextAlias(), b.nextAlias(), b.nextAlias()
	// Each node emits its opening/scalar token before children and its closing
	// token after them. Lexicographically ordered bigint paths preserve DFS
	// order without relying on physical JSON object enumeration order.
	return `(WITH RECURSIVE ` + walk + `(value,pointer,position,prefix) AS (
 SELECT ` + data + `,''::text,ARRAY[]::bigint[],''::text
 UNION ALL
 SELECT ` + child + `.value,
 ` + walk + `.pointer || '/' || replace(replace(` + child + `.key,'~','~0'),'/','~1'),
 ` + walk + `.position || (` + child + `.ordinal*2),
 (CASE WHEN ` + child + `.ordinal>1 THEN ',' ELSE '' END) || ` + child + `.prefix
 FROM ` + walk + ` CROSS JOIN LATERAL (
 SELECT a.value,(a.ordinal-1)::text AS key,a.ordinal,''::text AS prefix
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(` + walk + `.value)='array' THEN ` + walk + `.value ELSE '[]'::jsonb END) WITH ORDINALITY AS a(value,ordinal)
 UNION ALL
 SELECT o.value,o.key,row_number() OVER (ORDER BY o.key COLLATE "C"),to_json(o.key)::text || ':'
 FROM jsonb_each(CASE WHEN jsonb_typeof(` + walk + `.value)='object' THEN ` + walk + `.value ELSE '{}'::jsonb END) AS o(key,value)
 ) AS ` + child + `
 ), ` + tokens + ` AS (
 SELECT position || 0::bigint AS position,prefix || CASE jsonb_typeof(value)
 WHEN 'array' THEN '[' WHEN 'object' THEN '{'
 WHEN 'number' THEN COALESCE(` + metadata + `->'types'->pointer->>'text',
 CASE WHEN ` + metadata + `->'types'->pointer->>'type'='float' AND value::text !~ '[.eE]' THEN value::text || '.0'
 WHEN ` + metadata + `->'types'->pointer->>'type'='int' THEN (value::text::numeric)::bigint::text
 ELSE value::text END)
 ELSE COALESCE(value::text,'null') END AS token FROM ` + walk + `
 UNION ALL
 SELECT position || 9223372036854775807::bigint,CASE jsonb_typeof(value) WHEN 'array' THEN ']' ELSE '}' END
 FROM ` + walk + ` WHERE jsonb_typeof(value) IN ('array','object')
 ) SELECT string_agg(token,'' ORDER BY position)::json FROM ` + tokens + `)`
}

func (b *builder) checkJSONMetadata(binding Binding) (string, error) {
	if binding.invalidJSONOption {
		return "", configuration("invalid JSON binding option")
	}
	if binding.metadata == nil {
		return "", nil
	}
	if binding.format != JSONB {
		return "", configuration("type metadata requires a JSONB binding")
	}
	column, err := quoteColumn(*binding.metadata)
	if err != nil {
		return "", err
	}
	for _, part := range binding.metadata.parts {
		b.reserved[part] = true
	}
	return column, nil
}

// dynamicJSONMatches uses physical JSON classes and number spelling instead of
// guessing a subtype from numeric magnitude. JSONB needs metadata restoration
// before guards that distinguish int from float.
func (b *builder) dynamicJSONMatches(n hff.Node, code string, format JSONFormat, target hff.Type) (string, error) {
	class := string(format) + "_typeof(" + code + ")"
	switch target.Kind() {
	case hff.AnyType:
		return "TRUE", nil
	case hff.NeverType:
		return "FALSE", nil
	case hff.NullType:
		return "(" + code + " IS NULL OR " + class + "='null')", nil
	case hff.UnionType:
		var matches []string
		for _, member := range target.Members() {
			match, err := b.dynamicJSONMatches(n, code, format, member)
			if err != nil {
				return "", err
			}
			matches = append(matches, match)
		}
		return "(" + strings.Join(matches, " OR ") + ")", nil
	case hff.BoolType:
		return "(" + class + "='boolean')", nil
	case hff.StringType:
		return "(" + class + "='string')", nil
	case hff.IntType, hff.FloatType:
		if format == JSONB {
			return "", b.failure(n, "dynamic JSONB numeric guards require type metadata")
		}
		operator := "!~"
		if target.Kind() == hff.FloatType {
			operator = "~"
		}
		return "(" + class + "='number' AND (" + code + " #>> '{}'::text[]) " + operator + " '[.eE]')", nil
	case hff.ListType, hff.MapType:
		alias := b.nextAlias()
		container := "array"
		empty := "[]"
		function := "_array_elements"
		if target.Kind() == hff.MapType {
			container = "object"
			empty = "{}"
			function = "_each"
		}
		match, err := b.dynamicJSONMatches(n, alias+".value", format, target.Element())
		if err != nil {
			return "", err
		}
		safe := "(CASE WHEN " + class + "='" + container + "' THEN " + code + " ELSE '" + empty + "'::" + string(format) + " END)"
		return "(" + class + "='" + container + "' AND NOT EXISTS (SELECT 1 FROM " + string(format) + function + "(" + safe + ") AS " + alias + " WHERE NOT COALESCE(" + match + ",FALSE)))", nil
	default:
		return "", b.failure(n, "dynamic JSON guard cannot recover this HFF type")
	}
}

func (b *builder) jsonBindingCode(binding Binding) (string, JSONFormat, error) {
	code, err := quoteColumn(binding.column)
	if err != nil {
		return "", binding.format, err
	}
	metadata, err := b.checkJSONMetadata(binding)
	if err != nil {
		return "", binding.format, err
	}
	if metadata != "" {
		return b.restoreJSONMetadata(code, metadata), JSON, nil
	}
	return code, binding.format, nil
}

// Reserve caller-owned names before metadata restoration allocates any CTEs.
// Include later bindings and nested relation sources, not just the current row.
func (b *builder) reserveBindingIdentifiers(binding Binding) {
	reserve := func(column ColumnRef) {
		for _, part := range column.parts {
			b.reserved[part] = true
		}
	}
	reserve(binding.column)
	if binding.metadata != nil {
		reserve(*binding.metadata)
	}
	reservePredicate := func(predicate SQLPredicate) {
		for _, column := range predicate.references {
			reserve(column)
		}
	}
	if binding.structure != nil {
		reservePredicate(binding.structure.present)
		reservePredicate(binding.structure.null)
		for _, field := range binding.structure.fields {
			b.reserveBindingIdentifiers(field)
		}
	}
	if binding.relation != nil {
		c := binding.relation.config
		reserve(c.table)
		b.reserved[c.alias] = true
		reservePredicate(c.correlation)
		reservePredicate(c.present)
		reservePredicate(c.null)
		for _, order := range c.order {
			reserve(order.column)
		}
		b.reserveBindingIdentifiers(c.item)
	}
}
