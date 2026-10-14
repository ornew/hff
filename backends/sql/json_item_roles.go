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
	return b.memoizedItemRoles(n, true)
}

func (b *builder) jsonItemsRolesUncached(n hff.Node) *jsonItemRoles {
	if binding, ok := n.Binding(); ok {
		if parameter, exists := b.parameters[binding.ID()]; exists {
			return parameter.itemListRoles
		}
	}
	if n.Kind() == hff.GuardNode || n.Kind() == hff.TypeGuardNode {
		child, _ := n.Child(0)
		return b.jsonItemsRoles(child)
	}
	if n.Kind() == hff.ListNode && n.ChildCount() > 0 {
		var proof *jsonItemRoles
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			roles := b.jsonItemRoles(child)
			if roles == nil {
				if child.Type().Kind() == hff.NullType {
					continue
				}
				return nil
			}
			if proof != nil && jsonTaggedStringStateAmbiguous(proof.object, roles.object) {
				return nil
			}
			proof = mergeItemRoles(proof, roles)
		}
		return proof
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
	case "foldl", "foldr":
		return b.jsonFoldItemProvenance(n).list
	case "concat":
		argument, _ := n.Child(1)
		left, right := b.jsonItemsRoles(receiver), b.jsonItemsRoles(argument)
		if left == nil && !neutralItemState(b.jsonStoredType(receiver), true) || right == nil && !neutralItemState(b.jsonStoredType(argument), true) {
			return nil
		}
		return mergeItemRoles(left, right)
	case "map":
		callback, _ := n.Child(1)
		return b.jsonCallbackItemProvenance(callback, receiver).pair
	case "flatmap":
		callback, _ := n.Child(1)
		return b.jsonCallbackItemProvenance(callback, receiver).list
	case "filter", "distinct", "sort":
		return b.jsonItemsRoles(receiver)
	}
	return nil
}

func (b *builder) jsonItemRoles(n hff.Node) *jsonItemRoles {
	return b.memoizedItemRoles(n, false)
}

func (b *builder) jsonItemRolesUncached(n hff.Node) *jsonItemRoles {
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
	if method, ok := n.Method(); ok && (method.Name() == "foldl" || method.Name() == "foldr") {
		return b.jsonFoldItemProvenance(n).pair
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

// A list of pairs and a single pair are different representation proofs.
// These roles describe structure only: transformations may duplicate keys.
type jsonItemProvenance struct {
	pair, list *jsonItemRoles
}

func (b *builder) jsonCallbackItemProvenance(callback, receiver hff.Node) jsonItemProvenance {
	// Establish receiver roles first so recursive stored-type analysis can
	// reuse them inside this one lexical scope instead of retracing chains.
	roles := b.jsonItemsRoles(receiver)
	item := jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true)
	restore := b.jsonCallbackParameters(callback, []hff.Type{item}, []*jsonItemRoles{roles})
	defer restore()
	body, _ := callback.Child(0)
	return jsonItemProvenance{pair: b.jsonItemRoles(body), list: b.jsonItemsRoles(body)}
}

// Memoization lives only for one role-analysis call and its current lexical
// scope. Negative results matter too: ordinary nested maps must not trigger
// exponential repeated stored-type/role inference. Parameter scopes replace
// this memo and restore it on exit, so proofs never leak across bindings.
type jsonItemRoleMemo struct {
	pairs, lists map[hff.Node]*jsonItemRoles
	folds        map[hff.Node]jsonItemProvenance
}

func (b *builder) memoizedItemRoles(n hff.Node, list bool) *jsonItemRoles {
	owner := b.itemRoleMemo == nil
	if owner {
		b.itemRoleMemo = &jsonItemRoleMemo{
			pairs: make(map[hff.Node]*jsonItemRoles),
			lists: make(map[hff.Node]*jsonItemRoles),
			folds: make(map[hff.Node]jsonItemProvenance),
		}
		defer func() { b.itemRoleMemo = nil }()
	}
	cache := b.itemRoleMemo.pairs
	if list {
		cache = b.itemRoleMemo.lists
	}
	if roles, found := cache[n]; found {
		return roles
	}
	var roles *jsonItemRoles
	if list {
		roles = b.jsonItemsRolesUncached(n)
	} else {
		roles = b.jsonItemRolesUncached(n)
	}
	cache[n] = roles
	return roles
}
