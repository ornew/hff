package sql

import "github.com/ornew/hff"

// An immediate JSON observer can serialize each mapped value in its lexical
// item scope instead of merging distinct per-key tagged types into one schema.
func (b *builder) jsonMappedSlotSerialization(n hff.Node) (expression, bool, error) {
	receiver, _ := n.Child(0)
	source, stages := mapPipelineStages(receiver)
	if len(stages) == 0 || !b.mapPipelineHasItemSlot(source, stages) {
		return expression{}, false, nil
	}
	// Preserve established SQL and its complete validation when the ordinary
	// representation can already carry the mapped result.
	normal := b.validationBuilder()
	result, original := normal.call(n)
	if original == nil {
		b.adoptDeferredProbe(normal)
		return result, true, nil
	}
	if err := b.typedFallbackError(original); err != nil {
		return expression{}, true, err
	}
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	plan, err := probe.deferredList(receiver)
	if err == nil {
		value := &jsonDeferredValue{typ: probe.jsonStoredType(receiver), present: plan.present, list: &plan}
		result, err = probe.observeDeferredValue(n, value, "json")
	}
	if err != nil {
		return expression{}, true, err
	}
	b.adoptDeferredProbe(probe)
	return result, true, nil
}
