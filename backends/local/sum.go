package local

import (
	"math"
	"math/big"

	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

// Keep integer contributions exact until the final result is known. A Float
// contribution changes the accumulator to Go's native float64 model.
func (e *evaluation) sumList(values []core.Value, callback plan.Node, projected bool) (result, error) {
	var integers big.Int
	var floating float64
	hasFloat := false
	for _, item := range values {
		if err := e.charge(1); err != nil {
			return result{}, err
		}
		value := item
		if projected {
			computed, err := e.invoke(callback, item)
			if err != nil {
				return result{}, err
			}
			if !computed.present {
				continue
			}
			value = computed.value
		}
		switch value.Kind() {
		case core.NullKind:
			continue
		case core.IntKind:
			integer, _ := value.AsInt()
			if hasFloat {
				floating += float64(integer)
			} else {
				var term big.Int
				term.SetInt64(integer)
				integers.Add(&integers, &term)
			}
		case core.FloatKind:
			value, _ := value.AsFloat()
			if !hasFloat {
				previous, _ := new(big.Float).SetInt(&integers).Float64()
				floating = previous
				hasFloat = true
			}
			floating += value
		default:
			return null(), nil
		}
		if hasFloat && (math.IsNaN(floating) || math.IsInf(floating, 0)) {
			return null(), nil
		}
	}
	if hasFloat {
		return ordinary(core.Float(floating)), nil
	}
	if !integers.IsInt64() {
		return null(), nil
	}
	return ordinary(core.Int(integers.Int64())), nil
}
