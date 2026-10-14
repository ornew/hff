package local

import (
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

// Keep the first value at a numeric tie, including its Int or Float subtype.
func (e *evaluation) extremeList(values []core.Value, callback plan.Node, projected, minimum bool) (result, error) {
	var selected core.Value
	haveSelected := false
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
		if value.Kind() == core.NullKind {
			continue
		}
		if value.Kind() != core.IntKind && value.Kind() != core.FloatKind {
			return null(), nil
		}
		if !haveSelected {
			selected = value
			haveSelected = true
			continue
		}
		comparison := compareNumbers(value, selected)
		if minimum && comparison < 0 || !minimum && comparison > 0 {
			selected = value
		}
	}
	if !haveSelected {
		return null(), nil
	}
	return ordinary(selected), nil
}
