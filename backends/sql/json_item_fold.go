package sql

import (
	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

// A role invariant must cover the seed and every successor state. Null is
// ordinary state; an empty list is neutral only for a list of pairs, never
// for a single two-slot pair. Ambiguous origins cannot be merged by erasing
// a tagged value into Any or into another JSON string family.
func (b *builder) jsonFoldItemProvenance(n hff.Node) jsonItemProvenance {
	// Seed folds are visited through pair, list and stored-type analyses.
	// Share their combined result only within the current lexical memo scope.
	owner := b.itemRoleMemo == nil
	if owner {
		b.itemRoleMemo = &jsonItemRoleMemo{pairs: make(map[hff.Node]*jsonItemRoles), lists: make(map[hff.Node]*jsonItemRoles), folds: make(map[hff.Node]jsonItemProvenance)}
		defer func() { b.itemRoleMemo = nil }()
	}
	if b.itemRoleMemo.folds == nil {
		b.itemRoleMemo.folds = make(map[hff.Node]jsonItemProvenance)
	}
	memo := b.itemRoleMemo.folds
	if proof, found := memo[n]; found {
		return proof
	}
	proof := b.jsonFoldItemProvenanceUncached(n)
	memo[n] = proof
	return proof
}

func (b *builder) jsonFoldItemProvenanceUncached(n hff.Node) jsonItemProvenance {
	receiver, _ := n.Child(0)
	seed, _ := n.Child(1)
	callback, _ := n.Child(2)
	parameters, _ := callback.Parameters()
	if len(parameters) != 2 {
		return jsonItemProvenance{}
	}
	method, _ := n.Method()
	acc := 0
	if method.Name() == "foldr" {
		acc = 1
	}
	seedRoles := jsonItemProvenance{pair: b.jsonItemRoles(seed), list: b.jsonItemsRoles(seed)}
	seedType := b.jsonStoredType(seed)
	if seedRoles.pair == nil && seedRoles.list == nil && !neutralItemState(seedType, true) {
		return jsonItemProvenance{}
	}
	itemRoles := b.jsonItemsRoles(receiver)
	itemType := jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true)
	state, stateType := seedRoles, seedType
	for attempt := 0; attempt < 64; attempt++ {
		if checkContext(b.ctx) != nil {
			return jsonItemProvenance{}
		}
		types := []hff.Type{itemType, itemType}
		pairs := []*jsonItemRoles{itemRoles, itemRoles}
		lists := make([]*jsonItemRoles, 2)
		types[acc], pairs[acc], lists[acc] = stateType, state.pair, state.list
		restore := b.jsonCallbackParameters(callback, types, pairs, lists)
		body, _ := callback.Child(0)
		successor := jsonItemProvenance{pair: b.jsonItemRoles(body), list: b.jsonItemsRoles(body)}
		bodyType := b.jsonStoredType(body)
		restore()
		next := mergeItemState(seedRoles, seedType, successor, bodyType, b.runtimeRoleAnalysis)
		if next.pair == nil && next.list == nil {
			return next
		}
		if itemProvenanceKey(next) == itemProvenanceKey(state) {
			return next
		}
		state, stateType = next, itemProvenanceType(next)
	}
	return jsonItemProvenance{}
}

func neutralItemState(typ hff.Type, list bool) bool {
	return typ.Kind() == hff.NullType || list && typ.Kind() == hff.ListType && typ.Element().Kind() == hff.NeverType
}

func mergeItemState(seed jsonItemProvenance, seedType hff.Type, body jsonItemProvenance, bodyType hff.Type, runtime bool) jsonItemProvenance {
	if (seed.pair != nil || neutralItemState(seedType, false)) && (body.pair != nil || neutralItemState(bodyType, false)) {
		return jsonItemProvenance{pair: mergeItemRoles(seed.pair, body.pair, runtime)}
	}
	if (seed.list != nil || neutralItemState(seedType, true)) && (body.list != nil || neutralItemState(bodyType, true)) {
		return jsonItemProvenance{list: mergeItemRoles(seed.list, body.list, runtime)}
	}
	return jsonItemProvenance{}
}

func mergeItemRoles(left, right *jsonItemRoles, runtime bool) *jsonItemRoles {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if itemKeyType(left).Kind() == hff.NeverType || left.value.Kind() == hff.NeverType {
		return right
	}
	if itemKeyType(right).Kind() == hff.NeverType || right.value.Kind() == hff.NeverType {
		return left
	}
	if !runtime && jsonTaggedStringStateAmbiguous(left.object, right.object) {
		return nil
	}
	object := hff.NewUnionType(left.object, right.object)
	return &jsonItemRoles{object: object,
		key:   hff.Type(core.UnionTypes(core.Type(itemKeyType(left)), core.Type(itemKeyType(right)))),
		value: hff.Type(core.UnionTypes(core.Type(left.value), core.Type(right.value)))}
}

func itemProvenanceKey(proof jsonItemProvenance) string {
	if proof.pair != nil {
		return "pair:" + proof.pair.object.Key() + ":" + itemKeyType(proof.pair).Key() + ":" + proof.pair.value.Key()
	}
	if proof.list != nil {
		return "list:" + proof.list.object.Key() + ":" + itemKeyType(proof.list).Key() + ":" + proof.list.value.Key()
	}
	return ""
}

func itemPairType(roles *jsonItemRoles) hff.Type {
	return hff.NewListType(hff.Type(core.UnionTypes(core.Type(itemKeyType(roles)), core.Type(roles.value))))
}

func itemProvenanceType(proof jsonItemProvenance) hff.Type {
	null := hff.PrimitiveType(hff.NullType)
	if proof.pair != nil {
		return hff.NewUnionType(itemPairType(proof.pair), null)
	}
	if proof.list == nil {
		return hff.Type{}
	}
	return hff.NewUnionType(hff.NewListType(hff.NewUnionType(itemPairType(proof.list), null)), null)
}
