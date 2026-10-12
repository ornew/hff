package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// structuredMetadataSubpath restores only a literal field subtree. Metadata
// pointers start at the physical JSON document; flattened ancestors retain
// their ordinary missing/null states outside the restoration expression.
func (b *builder) structuredMetadataSubpath(n hff.Node, mapping Binding, fields []string, states []*structBinding) (expression, error) {
	code, err := quoteColumn(mapping.column)
	if err != nil {
		return expression{}, err
	}
	metadata, err := b.checkJSONMetadata(mapping)
	if err != nil {
		return expression{}, err
	}
	pointer := ""
	for _, field := range fields {
		code = b.project(code)
		key, err := b.parameter(n, hff.String(field))
		if err != nil {
			return expression{}, err
		}
		code = "(CASE WHEN jsonb_typeof(" + code + ")='object' THEN " + code + " -> " + key.code + "::text ELSE NULL END)"
		pointer += "/" + strings.ReplaceAll(strings.ReplaceAll(field, "~", "~0"), "/", "~1")
	}
	prefix, err := b.parameter(n, hff.String(pointer))
	if err != nil {
		return expression{}, err
	}
	code = b.restoreJSONMetadataAt(code, metadata, prefix.code+"::text")
	var absent []string
	for _, state := range states {
		absent = append(absent, "("+b.predicateCode(state.present)+") IS NOT TRUE", "("+b.predicateCode(state.null)+") IS TRUE")
	}
	if len(absent) > 0 {
		code = "(CASE WHEN " + strings.Join(absent, " OR ") + " THEN NULL::json ELSE " + code + " END)"
	}
	return b.jsonDecode(n, code, JSON, n.Type(), "TRUE")
}
