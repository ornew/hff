package sql

import "github.com/ornew/hff"

// Separate constructor positions retain identities that their homogeneous JSON
// element schema cannot express. Inspect children before Any can absorb an
// explicit tagged type in the merged public union. Stored values still have
// their own schema-recovery checks; this never guesses a tag from JSON text.
func (b *builder) deferredConstructorRoot(n hff.Node) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if n.Kind() != hff.ListNode {
		return false, nil
	}
	seen := map[string]bool{}
	var types []hff.Type
	for i := 0; i < n.ChildCount(); i++ {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		child, _ := n.Child(i)
		typ := b.jsonStoredType(child)
		if seen[typ.Key()] {
			continue
		}
		seen[typ.Key()] = true
		for _, previous := range types {
			if err := checkContext(b.ctx); err != nil {
				return false, err
			}
			if jsonTaggedStringStateAmbiguous(previous, typ) {
				return true, nil
			}
		}
		types = append(types, typ)
	}
	return false, nil
}

func (b *builder) hasDeferredConstructor(n hff.Node) (bool, error) {
	if needed, err := b.deferredConstructorRoot(n); needed || err != nil {
		return needed, err
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if needed, err := b.hasDeferredConstructor(child); needed || err != nil {
			return needed, err
		}
	}
	return false, nil
}
