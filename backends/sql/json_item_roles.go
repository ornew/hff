package sql

import (
	"math"

	"github.com/ornew/hff"
)

// items() produces two-element [String key, value] lists. Keep that positional
// fact private to lowering: the compiler's homogeneous list union and its
// capability checks are unchanged. No tag is inferred from a stored string.
type jsonItemRoles struct {
	value  hff.Type
	object hff.Type
}

// Only operations retaining complete source pairs may retain their roles.
func (b *builder) jsonItemsRoles(n hff.Node) *jsonItemRoles {
	if n.Kind() == hff.GuardNode || n.Kind() == hff.TypeGuardNode {
		child, _ := n.Child(0)
		return b.jsonItemsRoles(child)
	}
	method, ok := n.Method()
	if !ok {
		return nil
	}
	receiver, _ := n.Child(0)
	switch method.Name() {
	case "items":
		object := jsonObjectType(b.jsonStoredType(receiver))
		return &jsonItemRoles{value: jsonStoredMemberType(object, "", false, false), object: object}
	case "filter", "distinct", "sort":
		return b.jsonItemsRoles(receiver)
	}
	return nil
}

func (b *builder) jsonItemRoles(n hff.Node) *jsonItemRoles {
	if binding, ok := n.Binding(); ok {
		if parameter, exists := b.parameters[binding.ID()]; exists {
			return parameter.itemRoles
		}
	}
	switch n.Kind() {
	case hff.GuardNode, hff.TypeGuardNode:
		child, _ := n.Child(0)
		return b.jsonItemRoles(child)
	case hff.IndexNode:
		parent, _ := n.Child(0)
		return b.jsonItemsRoles(parent)
	}
	if method, ok := n.Method(); ok && method.Name() == "find" {
		receiver, _ := n.Child(0)
		return b.jsonItemsRoles(receiver)
	}
	return nil
}

// A literal slot has a fixed identity even when the public list element type
// is String|Bytes/Duration/Timestamp. Invalid or out-of-range literal indexes
// still yield ordinary null; a failed receiver guard keeps its own presence.
func (b *builder) jsonItemSlotType(parent, index hff.Node) (hff.Type, bool) {
	roles := b.jsonItemRoles(parent)
	if roles == nil {
		return hff.Type{}, false
	}
	x, known := itemLiteralIndex(index)
	if !known {
		return hff.Type{}, false
	}
	null := hff.PrimitiveType(hff.NullType)
	if x < 0 {
		x += 2
	}
	switch x {
	case 0:
		return hff.NewUnionType(hff.PrimitiveType(hff.StringType), null), true
	case 1:
		return hff.NewUnionType(roles.value, null), true
	default:
		return null, true
	}
}

func itemLiteralIndex(n hff.Node) (float64, bool) {
	if v, ok := n.Literal(); ok {
		if x, ok := v.AsInt(); ok {
			// Only -2, -1, 0 and 1 are in range, so precision loss for larger
			// int64 values cannot change the selected slot.
			return float64(x), true
		}
		if x, ok := v.AsFloat(); ok {
			if math.Trunc(x) == x {
				return x, true
			}
			return math.NaN(), true
		}
		if v.Kind() == hff.NullKind {
			return math.NaN(), true
		}
	}
	if n.Kind() == hff.NegateNode {
		child, _ := n.Child(0)
		x, known := itemLiteralIndex(child)
		return -x, known
	}
	return 0, false
}
