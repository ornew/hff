package local

import "github.com/ornew/hff/internal/core"

func (e *evaluation) uniqueList(values []core.Value) (result, error) {
	if len(values) < 2 {
		return ordinary(core.Bool(true)), nil
	}
	// Small inputs and an initial duplicate need no fingerprint walk or table.
	// Preserve the previous first comparison and its early failure behavior.
	equal, err := e.equal(values[1], values[0])
	if err != nil {
		return result{}, err
	}
	if equal || len(values) == 2 {
		return ordinary(core.Bool(!equal)), nil
	}
	seen := make(map[uint64][]int)
	for index, value := range values {
		key, err := e.valueFingerprint(value)
		if err != nil {
			return result{}, err
		}
		if index >= 2 {
			duplicate, err := e.equalCandidate(value, values, seen[key])
			if err != nil {
				return result{}, err
			}
			if duplicate {
				return ordinary(core.Bool(false)), nil
			}
		}
		seen[key] = append(seen[key], index)
	}
	return ordinary(core.Bool(true)), nil
}
