package local

import (
	"math"
	"math/big"

	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

// Keep integer contributions exact until the final result is known. A Float
// contribution changes the accumulator to Go's native float64 model.
type numericAccumulation struct {
	integers big.Int
	floating float64
	count    int64
	hasFloat bool
	invalid  bool
}

func (e *evaluation) accumulateList(values []core.Value, callback plan.Node, projected bool) (numericAccumulation, error) {
	var total numericAccumulation
	for _, item := range values {
		if err := e.charge(1); err != nil {
			return numericAccumulation{}, err
		}
		value := item
		if projected {
			computed, err := e.invoke(callback, item)
			if err != nil {
				return numericAccumulation{}, err
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
			if total.hasFloat {
				total.floating += float64(integer)
			} else {
				var term big.Int
				term.SetInt64(integer)
				total.integers.Add(&total.integers, &term)
			}
		case core.FloatKind:
			value, _ := value.AsFloat()
			if !total.hasFloat {
				previous, _ := new(big.Float).SetInt(&total.integers).Float64()
				total.floating = previous
				total.hasFloat = true
			}
			total.floating += value
		default:
			total.invalid = true
			return total, nil
		}
		total.count++
		if total.hasFloat && (math.IsNaN(total.floating) || math.IsInf(total.floating, 0)) {
			total.invalid = true
			return total, nil
		}
	}
	return total, nil
}

func (e *evaluation) sumList(values []core.Value, callback plan.Node, projected bool) (result, error) {
	total, err := e.accumulateList(values, callback, projected)
	if err != nil {
		return result{}, err
	}
	if total.invalid {
		return null(), nil
	}
	if total.hasFloat {
		return ordinary(core.Float(total.floating)), nil
	}
	if !total.integers.IsInt64() {
		return null(), nil
	}
	return ordinary(core.Int(total.integers.Int64())), nil
}

func (e *evaluation) avgList(values []core.Value, callback plan.Node, projected bool) (result, error) {
	total, err := e.accumulateList(values, callback, projected)
	if err != nil {
		return result{}, err
	}
	if total.invalid || total.count == 0 {
		return null(), nil
	}
	value := total.floating
	if !total.hasFloat {
		value, _ = new(big.Float).SetInt(&total.integers).Float64()
	}
	value /= float64(total.count)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return null(), nil
	}
	return ordinary(core.Float(value)), nil
}
