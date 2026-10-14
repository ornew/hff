package sql

import (
	"errors"

	"github.com/ornew/hff"
)

// Reuse bounded key/position equality inside membership's element scope. Any
// leaves retain their generic comparison, and the previous representation
// contract is checked before avoiding whole-document normalization.
func (b *builder) boundedJSONMembershipEqual(n hff.Node, left string, lf JSONFormat, lt hff.Type, right string, rf JSONFormat, rt hff.Type) (string, bool, error) {
	if !sharedJSONContainer(lt, rt) || makeComparisonShape(lt).any || makeComparisonShape(rt).any {
		return "", false, nil
	}
	if err := checkContext(b.ctx); err != nil {
		return "", false, err
	}
	// The old JSONB/non-numeric path leaves numeric data intact rather than
	// turning a rejected Float decode into nested null. It must not acquire
	// schemaJSONCompare's normalization semantics as a side effect of pruning.
	if keepRawJSONComparisonNumbers(lf, lt, rt) || keepRawJSONComparisonNumbers(rf, rt, lt) {
		return "", false, nil
	}
	for _, side := range []struct {
		format JSONFormat
		typ    hff.Type
	}{{lf, lt}, {rf, rt}} {
		if err := b.checkComparisonDocumentSchema(n, side.format, side.typ); err != nil {
			var detail *hff.Error
			if errors.As(err, &detail) && detail.Code == hff.BackendError {
				// The old path decides whether this representation failure
				// is relevant to the actual counterpart. Cancellation and
				// any unexpected failure must still propagate immediately.
				return "", false, nil
			}
			return "", false, err
		}
	}
	code, err := b.schemaJSONCompare(n, left, lf, lt, lt, right, rf, rt, rt, "", hff.Equal)
	return code, true, err
}
