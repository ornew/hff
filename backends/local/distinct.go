package local

import (
	"math"

	"github.com/ornew/hff/internal/core"
)

// Fingerprints partition candidates; equality always verifies collisions. The
// fixed hash keeps work accounting deterministic and the first value intact.
func (e *evaluation) distinctList(receiver core.Value) (result, error) {
	values, _ := receiver.ListView()
	if err := e.allocate(1); err != nil {
		return result{}, err
	}
	seen := make(map[uint64][]int)
	var output []core.Value
	for _, value := range values {
		key, err := e.valueFingerprint(value)
		if err != nil {
			return result{}, err
		}
		duplicate, err := e.equalCandidate(value, output, seen[key])
		if err != nil {
			return result{}, err
		}
		if duplicate {
			continue
		}
		if err := e.allocate(1); err != nil {
			return result{}, err
		}
		seen[key] = append(seen[key], len(output))
		output = append(output, value)
	}
	return ordinary(core.OwnedList(output)), nil
}

// A fingerprint is only a candidate selector. Verify every bucket collision
// with HFF equality, including the work and cancellation checks it performs.
func (e *evaluation) equalCandidate(value core.Value, values []core.Value, candidates []int) (bool, error) {
	for _, index := range candidates {
		equal, err := e.equal(value, values[index])
		if err != nil || equal {
			return equal, err
		}
	}
	return false, nil
}

const fingerprintOffset uint64 = 14695981039346656037
const fingerprintPrime uint64 = 1099511628211

func fingerprintUint(hash, value uint64) uint64 {
	for i := 0; i < 8; i++ {
		hash = (hash ^ (value & 255)) * fingerprintPrime
		value >>= 8
	}
	return hash
}

func fingerprintString(hash uint64, value string) uint64 {
	hash = fingerprintUint(hash, uint64(len(value)))
	for i := 0; i < len(value); i++ {
		hash = (hash ^ uint64(value[i])) * fingerprintPrime
	}
	return hash
}

func (e *evaluation) valueFingerprint(value core.Value) (uint64, error) {
	if err := e.charge(1); err != nil {
		return 0, err
	}
	kind := value.Kind()
	var number uint64
	if integer, ok := value.AsInt(); ok {
		number = uint64(integer)
	} else if floating, ok := value.AsFloat(); ok {
		// Equal numeric values share a bucket without rounding large Ints.
		if floating >= -0x1p63 && floating < 0x1p63 && math.Trunc(floating) == floating {
			kind, number = core.IntKind, uint64(int64(floating))
		} else {
			number = math.Float64bits(floating)
		}
	}
	hash := fingerprintUint(fingerprintOffset, uint64(kind))
	switch kind {
	case core.IntKind, core.FloatKind:
		hash = fingerprintUint(hash, number)
	case core.BoolKind:
		v, _ := value.AsBool()
		if v {
			hash = fingerprintUint(hash, 1)
		}
	case core.StringKind:
		v, _ := value.AsString()
		if err := e.charge(len(v)); err != nil {
			return 0, err
		}
		hash = fingerprintString(hash, v)
	case core.BytesKind:
		v, _ := value.BytesView()
		if err := e.charge(len(v)); err != nil {
			return 0, err
		}
		hash = fingerprintUint(hash, uint64(len(v)))
		for _, b := range v {
			hash = (hash ^ uint64(b)) * fingerprintPrime
		}
	case core.TimestampKind:
		v, _ := value.AsTimestamp()
		hash = fingerprintUint(fingerprintUint(hash, uint64(v.Unix())), uint64(v.Nanosecond()))
	case core.DurationKind:
		v, _ := value.AsDuration()
		hash = fingerprintUint(hash, uint64(v))
	case core.ListKind:
		v, _ := value.ListView()
		hash = fingerprintUint(hash, uint64(len(v)))
		for _, item := range v {
			child, err := e.valueFingerprint(item)
			if err != nil {
				return 0, err
			}
			hash = fingerprintUint(hash, child)
		}
	case core.ObjectKind:
		v, _ := value.ObjectView()
		keys, err := e.objectKeys(v)
		if err != nil {
			return 0, err
		}
		hash = fingerprintUint(hash, uint64(len(keys)))
		for _, key := range keys {
			child, err := e.valueFingerprint(v[key])
			if err != nil {
				return 0, err
			}
			hash = fingerprintUint(fingerprintString(hash, key), child)
		}
	}
	return hash, nil
}
