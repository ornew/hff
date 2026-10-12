package sql

import "github.com/ornew/hff"

// Callback receivers are checked before evaluating their bodies. Keep the
// physical format so unused elements do not impose restoration requirements;
// operations in the body and retained results enforce their own requirements.
func (b *builder) jsonCallbackReceiver(n hff.Node, value expression) (expression, JSONFormat, error) {
	format, ok := b.jsonValues[value.code]
	if !ok || value.kind != hff.ListType && value.kind != hff.AnyType {
		return expression{}, "", b.failure(n, "collection callbacks require a recoverable JSON list receiver")
	}
	if value.kind == hff.AnyType {
		code := "(CASE WHEN " + string(format) + "_typeof(" + value.code + ")='array' THEN " + value.code + " ELSE NULL::" + string(format) + " END)"
		b.jsonValues[code] = format
		value = expression{code, value.present, hff.ListType}
	}
	return value, format, nil
}
