package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// A nonnull literal seed and literal chunks cannot turn the accumulator into
// ordinary null. Thus the first no-value failure is equivalent to the combined
// chunk presence, while scalar nulls remain ordinary list elements.
func (b *builder) listBuildingFoldProof(n hff.Node, name string) (hff.Node, bool, error) {
	if b.disableListBuildingFold {
		return hff.Node{}, false, nil
	}
	seed, _ := n.Child(1)
	if seed.Kind() != hff.ListNode || seed.ChildCount() != 0 {
		return hff.Node{}, false, nil
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return hff.Node{}, false, nil
	}
	body, _ := callback.Child(0)
	if !builtinListCall(body, "concat") || body.ChildCount() != 2 {
		return hff.Node{}, false, nil
	}
	acc := 0
	if name == "foldr" {
		acc = 1
	}
	id := parameters[acc].ID()
	left, _ := body.Child(0)
	right, _ := body.Child(1)
	chunk, prepend := right, false
	if binding, ok := left.Binding(); !ok || binding.ID() != id {
		binding, ok := right.Binding()
		if !ok || binding.ID() != id {
			return hff.Node{}, false, nil
		}
		chunk, prepend = left, true
	}
	if chunk.Kind() != hff.ListNode || chunk.ChildCount() == 0 {
		return hff.Node{}, false, nil
	}
	var independent func(hff.Node) (bool, error)
	independent = func(node hff.Node) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if binding, ok := node.Binding(); ok && binding.ID() == id {
			return false, nil
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if ok, err := independent(child); err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	}
	if ok, err := independent(chunk); err != nil || !ok {
		return hff.Node{}, false, err
	}
	if ok, err := b.builtinDeferredFoldProjection(n); err != nil || !ok {
		return hff.Node{}, false, err
	}
	return chunk, prepend != (name == "foldr"), nil
}

// Keep the original source's runtime encoding boundary. Eliminating it could
// expose an unnormalized JSON Any value to the callback instead of the value
// the recursive fold would have received.
func (b *builder) runtimeListBuildingFold(n, chunk hff.Node, binding hff.Binding, receiver hff.Node, plan jsonDeferredList, rows []string, items, item, final string, reverseChunks, receiverLoss, loss bool, provenance jsonItemProvenance) (*jsonDeferredValue, error) {
	outerSteps, outerPaths, outerParameters, outerMemo := b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo
	b.steps, b.sharedPaths, b.itemRoleMemo = nil, nil, nil
	b.parameters = copyJSONParameters(outerParameters)
	restore := func() {
		b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo = outerSteps, outerPaths, outerParameters, outerMemo
	}
	defer restore()
	itemProvenance, err := b.runtimeItemProvenance(receiver)
	if err != nil {
		return nil, err
	}
	input := runtimeValue(item+".value", jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true), "TRUE", receiverLoss)
	input.runtime.itemRoles = itemProvenance.list
	b.parameters[binding.ID()] = deferredValueParameter(input)
	var code, present string
	err = b.validateEmptyDeferredWork(len(rows) == 0, func() error {
		value, err := b.deferredValue(chunk)
		if err != nil {
			return err
		}
		present = deferredParameterPresence(value)
		code, err = b.encodeRuntimeValue(chunk, deferredLeaf(value))
		return err
	})
	if err != nil {
		return nil, err
	}
	steps := append([]string(nil), b.steps...)
	restore()
	if len(rows) == 0 {
		value := runtimeValue("(CASE WHEN "+plan.null+" THEN "+runtimeNull+" ELSE "+runtimeNode("list", "'[]'::json")+" END)", n.Type(), plan.present, loss)
		value.runtime.itemRoles, value.runtime.itemListRoles = provenance.pair, provenance.list
		return value, nil
	}
	chunks, elements := b.nextAlias(), b.nextAlias()
	from := items + " AS " + item
	if len(steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(steps, " CROSS JOIN LATERAL ")
	}
	order := "ASC"
	if reverseChunks {
		order = "DESC"
	}
	cte := "WITH " + items + " AS MATERIALIZED (" + strings.Join(rows, " UNION ALL ") + ")," + chunks + " AS MATERIALIZED (SELECT " + item + ".ord," + code + " AS value," + present + " AS p FROM " + from + ")"
	payload := "COALESCE(json_agg(" + elements + ".value ORDER BY " + chunks + ".ord " + order + "," + elements + ".ord),'[]'::json)"
	presence := "COALESCE(bool_and(COALESCE(" + chunks + ".p,FALSE)),TRUE)"
	b.steps = append(b.steps, "("+cte+" SELECT "+runtimeNode("list", payload)+" AS v,"+presence+" AS p FROM "+chunks+" CROSS JOIN LATERAL json_array_elements("+chunks+".value->1) WITH ORDINALITY AS "+elements+"(value,ord) OFFSET 0) AS "+final)
	value := runtimeValue("(CASE WHEN "+plan.null+" THEN "+runtimeNull+" ELSE "+final+".v END)", n.Type(), both(plan.present, "("+plan.null+" OR "+final+".p)"), loss)
	value.runtime.itemRoles, value.runtime.itemListRoles = provenance.pair, provenance.list
	// Keep the adopted aggregate after the lexical callback environment ends.
	outerSteps = b.steps
	return value, nil
}
