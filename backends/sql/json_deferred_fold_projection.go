package sql

import "github.com/ornew/hff"

// An unused item is a lexical property, not a spelling or type property.
// Traverse nested callbacks too: they can capture the outer item even when
// their own parameter has a different name.
func (b *builder) unusedDeferredFoldInput(n hff.Node, name string) (bool, error) {
	if b.disableDeferredFoldPruning {
		return false, nil
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return false, nil
	}
	item := 1
	if name == "foldr" {
		item = 0
	}
	var uses func(hff.Node) (bool, error)
	uses = func(node hff.Node) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if binding, ok := node.Binding(); ok && binding.ID() == parameters[item].ID() {
			return true, nil
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if used, err := uses(child); err != nil || used {
				return used, err
			}
		}
		return false, nil
	}
	body, _ := callback.Child(0)
	if used, err := uses(body); err != nil || used {
		return false, err
	}
	return b.builtinDeferredFoldProjection(n)
}

// Keep the original static type for callback provenance, but do not decode
// a value proven to have no references in the callback. The input's row,
// ordinal and presence still come from its original deferred list plan.
func unusedDeferredFoldPacket(typ hff.Type) jsonDeferredPacket {
	return jsonDeferredPacket{code: "'null'::json", typ: typ,
		decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
			return deferredLeaf(jsonParameter{typ: typ, code: code, format: JSON}), checkContext(b.ctx)
		},
	}
}

func (b *builder) builtinDeferredFoldProjection(n hff.Node) (bool, error) {
	// The validation probe repeats packet, seed and body lowering. Custom
	// translators must retain their established invocation behavior, even
	// when their result is not read by this callback.
	var builtins func(hff.Node) (bool, error)
	builtins = func(node hff.Node) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if method, ok := node.Method(); ok {
			for _, signature := range method.Signatures() {
				if _, custom := signature.Declaration(); custom {
					return false, nil
				}
			}
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if pure, err := builtins(child); err != nil || !pure {
				return false, err
			}
		}
		return true, nil
	}
	return builtins(n)
}

// A first-level field is enough to keep the entire selected child. Consume
// each direct field access before visiting its binding, so a bare use anywhere
// else (including a nested capture) makes projection ineligible.
func (b *builder) observedDeferredFoldFields(n hff.Node, name string) (map[string]bool, error) {
	if b.disableDeferredFoldPruning {
		return nil, nil
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return nil, nil
	}
	item := 1
	if name == "foldr" {
		item = 0
	}
	id := parameters[item].ID()
	fields := map[string]bool{}
	var scan func(hff.Node) (bool, error)
	scan = func(node hff.Node) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if node.Kind() == hff.FieldNode || node.Kind() == hff.IndexNode {
			parent, _ := node.Child(0)
			if binding, ok := parent.Binding(); ok && binding.ID() == id {
				if key, ok := node.Field(); ok {
					fields[key] = true
					return true, nil
				}
				index, _ := node.Child(1)
				if value, ok := index.Literal(); ok {
					if key, ok := value.AsString(); ok {
						fields[key] = true
						return true, nil
					}
				}
				return false, nil
			}
		}
		if binding, ok := node.Binding(); ok && binding.ID() == id {
			return false, nil
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if ok, err := scan(child); err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	}
	body, _ := callback.Child(0)
	if ok, err := scan(body); err != nil || !ok || len(fields) == 0 {
		return nil, err
	}
	if ok, err := b.builtinDeferredFoldProjection(n); err != nil || !ok {
		return nil, err
	}
	return fields, nil
}

func (b *builder) hasUnobservedDeferredFoldFields(plan jsonDeferredList, observed map[string]bool) (bool, error) {
	for _, arm := range plan.arms {
		value := deferredLeaf(arm.value)
		if value.object == nil {
			continue
		}
		for _, field := range *value.object {
			if err := checkContext(b.ctx); err != nil {
				return false, err
			}
			if !observed[field.name] {
				return true, nil
			}
		}
	}
	return false, nil
}
