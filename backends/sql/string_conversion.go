package sql

import "github.com/ornew/hff"

// stringConversionInput admits only the string branch of a dynamic receiver.
// An inapplicable runtime family becomes ordinary null without a coercion.
func (b *builder) stringConversionInput(n hff.Node, receiver expression) (string, error) {
	switch receiver.kind {
	case hff.StringType:
		return receiver.code, nil
	case hff.AnyType:
		format, ok := b.jsonValues[receiver.code]
		if !ok {
			return "", b.failure(n, "string conversion requires a recoverable JSON receiver")
		}
		return "(CASE WHEN " + string(format) + "_typeof(" + receiver.code + ")='string' THEN (" + receiver.code + " #>> '{}'::text[]) ELSE NULL::text END)", nil
	default:
		return "NULL::text", nil
	}
}

func (b *builder) primitiveConversion(n hff.Node, name string, receiver expression) (expression, error) {
	if n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	text, err := b.stringConversionInput(n, receiver)
	if err != nil {
		return expression{}, err
	}
	if name == "bool" {
		code := "(CASE WHEN " + text + " COLLATE \"C\"='true' THEN TRUE WHEN " + text + " COLLATE \"C\"='false' THEN FALSE ELSE NULL::boolean END)"
		return expression{code, receiver.present, hff.BoolType}, nil
	}
	// Validate JSON number spelling before any numeric lexer or cast sees it.
	// Unlike fromjson(), the conversion never trims surrounding whitespace.
	raw := b.project("(" + text + " COLLATE \"C\")")
	safe := b.project("(CASE WHEN " + raw + " ~ '^-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?$' THEN " + raw + " ELSE NULL::text END)")
	integer := "(CASE WHEN pg_input_is_valid(" + safe + ",'bigint') THEN " + safe + " ELSE NULL::text END)::bigint"
	code := "(CASE WHEN " + safe + " !~ '[.eE]' THEN to_json(" + integer + ") ELSE " + b.floatText(parseJSONFloat(safe)) + "::json END)"
	return b.jsonDecode(n, code, JSON, n.Type(), receiver.present)
}
