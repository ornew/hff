package sql

import "github.com/ornew/hff"

// computedJSONDocument encodes callback state without decoding a scalar that
// was just encoded from its native SQL carrier. A JSON carrier, container or
// wider stored type still needs ordinary schema-guided normalization.
func (b *builder) computedJSONDocument(n hff.Node, value expression, stored hff.Type) (string, error) {
	raw, format, err := b.jsonRepresentation(n, value)
	if err != nil {
		return "", err
	}
	if err := b.checkComparisonDocumentSchema(n, format, stored); err != nil {
		return "", err
	}
	_, jsonCarrier := b.jsonValues[value.code]
	kind, known := jsonKind(stored)
	if !jsonCarrier && known && kind == value.kind {
		switch kind {
		case hff.BoolType, hff.IntType, hff.FloatType, hff.StringType,
			hff.BytesType, hff.DurationType, hff.TimestampType:
			// Native SQL NULL is a JSON null element or accumulator, not a
			// missing value. Presence remains separate in the caller.
			return "COALESCE(" + raw + ",'null'::json)", nil
		}
	}
	return b.normalizedComparisonDocument(n, raw, format, stored)
}
