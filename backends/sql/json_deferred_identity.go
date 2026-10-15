package sql

import "github.com/ornew/hff"

// Ordinary JSON is an optimization only when every value at a structural
// position has one schema. This deliberately stronger proof avoids relying on
// union canonicalization or JSON spelling to recover numeric or tagged kinds.
// Different schemas may still be representable, but use typed packets until a
// more precise proof establishes that their conversion preserves identity.
func (b *builder) deferredInputIdentity(value *jsonDeferredValue) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	var types []hff.Type
	visit := func(child *jsonDeferredValue) (bool, error) { return b.deferredInputIdentity(child) }
	switch {
	case value.leaf != nil:
		if slot := value.leaf.itemSlot; slot != nil {
			types = itemSlotTypes(slot.roles, slot.key != "FALSE", slot.value != "FALSE", slot.constraints)
		} else {
			return true, nil
		}
	case value.object != nil:
		for _, field := range *value.object {
			if safe, err := visit(field.value); !safe || err != nil {
				return safe, err
			}
		}
		return true, nil
	case value.choices != nil:
		for _, choice := range value.choices {
			if safe, err := visit(choice.value); !safe || err != nil {
				return safe, err
			}
			types = append(types, choice.value.typ)
		}
	case value.list != nil || value.selection != nil:
		var arms []jsonDeferredArm
		if value.list != nil {
			arms = value.list.arms
		} else {
			arms = value.selection.arms
			if safe, err := visit(value.selection.fallback); !safe || err != nil {
				return safe, err
			}
			types = append(types, value.selection.fallback.typ)
		}
		for _, arm := range arms {
			if arm.empty {
				continue
			}
			child := deferredLeaf(arm.value)
			if safe, err := visit(child); !safe || err != nil {
				return safe, err
			}
			types = append(types, child.typ)
		}
	default:
		return false, nil
	}
	key := ""
	for _, typ := range types {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if typ.Kind() == hff.UnionType {
			var members []hff.Type
			for _, member := range typ.Members() {
				if member.Kind() != hff.NullType {
					members = append(members, member)
				}
			}
			typ = hff.NewUnionType(members...)
		}
		if typ.Kind() == hff.NullType || typ.Kind() == hff.NeverType {
			continue
		}
		if key != "" && key != typ.Key() {
			return false, nil
		}
		key = typ.Key()
	}
	return true, nil
}
