package sql

import (
	"errors"

	"github.com/ornew/hff"
)

// normalizedJSON proves schema-guided representation normalization, not
// canonical serialization or object-key ordering. It describes one immutable
// SQL value; presence and Float-loss provenance remain separate.
type normalizedJSON struct {
	format  JSONFormat
	typeKey string
	stored  hff.Type
}

func (b *builder) rememberNormalizedJSON(code string, format JSONFormat, typ hff.Type) {
	if format != JSON {
		return
	}
	if b.normalizedJSONValues == nil {
		b.normalizedJSONValues = make(map[string]normalizedJSON)
	}
	b.normalizedJSONValues[code] = normalizedJSON{format: format, typeKey: typ.Key(), stored: typ}
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

// A compatible target schema may widen normalized subtypes, but must leave
// every represented value unchanged. The physical carrier must still match.
func (b *builder) canReuseNormalizedJSON(code string, format JSONFormat, typ hff.Type) (bool, error) {
	proof, ok := b.normalizedJSONValues[code]
	if !ok || format != JSON || proof.format != format {
		return false, nil
	}
	if b.isNormalizedJSON(code, format, typ) {
		return true, checkContext(b.ctx)
	}
	return b.normalizationPreservesJSON(proof.stored, typ)
}

// Output proof is optional: a schema restriction must retain the existing
// lowering path rather than turn an optimization into a new Build rejection.
// Cancellation and unexpected failures remain observable.
func (b *builder) normalizationProofSchema(n hff.Node, typ hff.Type) (bool, error) {
	err := b.checkComparisonDocumentSchema(n, JSON, typ)
	if err == nil {
		return true, nil
	}
	var detail *hff.Error
	if errors.As(err, &detail) && detail.Code == hff.BackendError {
		return false, nil
	}
	return false, err
}
