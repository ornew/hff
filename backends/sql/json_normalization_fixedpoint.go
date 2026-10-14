package sql

import "github.com/ornew/hff"

// normalizationPreservesJSON proves that target normalization leaves a value
// already normalized under source unchanged. This is a representation proof,
// not type assignability: null is always preserved, and struct normalization
// retains actual keys while normalizing unknown values under its fallback.
// Both schemas must pass the ordinary stored-representation checks first.
func (b *builder) normalizationPreservesJSON(source, target hff.Type) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if source.Key() == target.Key() {
		return true, nil
	}
	from, to := makeComparisonShape(source), makeComparisonShape(target)
	if to.any {
		return true, nil
	}
	if from.any {
		return false, nil
	}
	for _, member := range from.members {
		switch kind := member.Kind(); kind {
		case hff.NullType, hff.NeverType, hff.ListType, hff.MapType, hff.StructType:
			continue
		default:
			found := false
			for _, candidate := range to.members {
				if candidate.Kind() == kind {
					found = true
					break
				}
			}
			if !found {
				return false, nil
			}
		}
	}
	if from.list {
		if !to.list {
			return false, nil
		}
		stable, err := b.normalizationPreservesJSON(from.element, to.element)
		if err != nil || !stable {
			return false, err
		}
	}
	if from.object {
		if !to.object {
			return false, nil
		}
		// A target-only key used the source fallback; a source-only key uses the
		// target fallback. Check both before the unknown-key fallback pair.
		for key, typ := range from.fields {
			other, exists := to.fields[key]
			if !exists {
				other = to.fallback
			}
			stable, err := b.normalizationPreservesJSON(typ, other)
			if err != nil || !stable {
				return false, err
			}
		}
		for key, typ := range to.fields {
			if _, exists := from.fields[key]; exists {
				continue
			}
			stable, err := b.normalizationPreservesJSON(from.fallback, typ)
			if err != nil || !stable {
				return false, err
			}
		}
		stable, err := b.normalizationPreservesJSON(from.fallback, to.fallback)
		if err != nil || !stable {
			return false, err
		}
	}
	return true, nil
}
