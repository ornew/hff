package sql

import "github.com/ornew/hff"

// Valid object storage has unique keys, so unchanged items are also distinct
// regardless of their value types. This proof is separate from positional
// pair roles: replacing or duplicating a pair invalidates source uniqueness.
// It never skips receiver lowering, NULL/no-value propagation or type checks.
func (b *builder) jsonItemsUnique(n hff.Node) bool {
	n = unguardedItemNode(n)
	method, ok := n.Method()
	if !ok {
		return false
	}
	receiver, _ := n.Child(0)
	switch method.Name() {
	case "items":
		return true
	case "filter", "sort":
		return b.jsonItemsUnique(receiver)
	case "distinct":
		// distinct() establishes its own uniqueness even for duplicated
		// source pairs. Keep this optimization within proven item lists.
		return b.jsonItemsRoles(receiver) != nil
	case "map", "flatmap":
		if !b.jsonItemsUnique(receiver) {
			return false
		}
		callback, _ := n.Child(1)
		parameters, _ := callback.Parameters()
		if len(parameters) != 1 {
			return false
		}
		body, _ := callback.Child(0)
		body = unguardedItemNode(body)
		if method.Name() == "flatmap" {
			if body.Kind() != hff.ListNode || body.ChildCount() != 1 {
				return false
			}
			body, _ = body.Child(0)
			body = unguardedItemNode(body)
		}
		binding, ok := body.Binding()
		return ok && binding.ID() == parameters[0].ID()
	}
	return false
}

func unguardedItemNode(n hff.Node) hff.Node {
	for n.Kind() == hff.GuardNode || n.Kind() == hff.TypeGuardNode {
		n, _ = n.Child(0)
	}
	return n
}
