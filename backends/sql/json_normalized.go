package sql

import "github.com/ornew/hff"

// normalizedJSON proves schema-guided representation normalization, not
// canonical serialization or object-key ordering. It describes one immutable
// SQL value; presence and Float-loss provenance remain separate.
type normalizedJSON struct {
	format  JSONFormat
	typeKey string
}

func (b *builder) rememberNormalizedJSON(code string, format JSONFormat, typ hff.Type) {
	if format != JSON {
		return
	}
	if b.normalizedJSONValues == nil {
		b.normalizedJSONValues = make(map[string]normalizedJSON)
	}
	b.normalizedJSONValues[code] = normalizedJSON{format, typ.Key()}
}

// Only ordinary value projections and SQL/JSON null normalization preserve
// this proof. Extraction, guards and casts require their own representation
// and type proof rather than inheriting it from a parent document.
func (b *builder) copyNormalizedJSON(source, target string, format JSONFormat) {
	proof, ok := b.normalizedJSONValues[source]
	if !ok || proof.format != format {
		return
	}
	b.normalizedJSONValues[target] = proof
}

func (b *builder) isNormalizedJSON(code string, format JSONFormat, typ hff.Type) bool {
	proof, ok := b.normalizedJSONValues[code]
	return ok && format == JSON && proof.format == format && proof.typeKey == typ.Key()
}
