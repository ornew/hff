package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// Identical pure expressions in one lexical environment have one value
// descriptor and several output positions. Compare typed IR and backend order
// demand, never just public types or generated SQL text.
func (b *builder) repeatedDeferredList(n hff.Node) (jsonDeferredList, bool, error) {
	if b.disableDeferredRepeat {
		return jsonDeferredList{}, false, nil
	}
	groups, compressed, err := b.deferredRepeatGroups(n)
	if err != nil || !compressed {
		return jsonDeferredList{}, false, err
	}
	// Keep the original constructor validation, including every child and
	// lexical lookup, without leaking discarded parameters or projections.
	probe := b.validationBuilder()
	// Constructor children can contain more constructors. Disable sharing
	// throughout validation, so duplicate wrappers are traversed once rather
	// than recursively adding a new validation pass at every nested level.
	probe.disableDeferredRepeat = true
	if _, err := probe.deferredListConstructor(n); err != nil {
		return jsonDeferredList{}, true, err
	}
	plan := jsonDeferredList{null: "FALSE", present: "TRUE"}
	for _, group := range groups {
		value, err := b.deferredValue(group.first)
		if err != nil {
			return jsonDeferredList{}, true, err
		}
		plan.present = both(plan.present, deferredParameterPresence(value))
		positions := make([]string, len(group.positions))
		for i, position := range group.positions {
			if err := checkContext(b.ctx); err != nil {
				return jsonDeferredList{}, true, err
			}
			positions[i] = "(" + strconv.Itoa(position) + ")"
		}
		alias := b.nextAlias()
		for _, position := range group.positions {
			for len(plan.literalPositions) <= position {
				plan.literalPositions = append(plan.literalPositions, 0)
			}
			plan.literalPositions[position] = len(plan.arms)
		}
		plan.arms = append(plan.arms, jsonDeferredArm{
			from:    "(VALUES " + strings.Join(positions, ",") + ") AS " + alias + "(ord)",
			ordinal: []string{alias + ".ord"}, selected: "TRUE", value: value,
			parameters: copyJSONParameters(b.parameters),
		})
	}
	// literal must remain false: an arm can emit several positions. Literal
	// indexing instead treats each arm as exactly one element.
	return plan, true, nil
}

type deferredRepeatGroup struct {
	first     hff.Node
	positions []int
}

func (b *builder) deferredRepeatGroups(n hff.Node) ([]deferredRepeatGroup, bool, error) {
	var leaves []hff.Node
	var collect func(hff.Node) (bool, error)
	collect = func(n hff.Node) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if n.Kind() == hff.ListNode {
			if n.ChildCount() == 0 {
				return false, nil
			}
			for i := 0; i < n.ChildCount(); i++ {
				child, _ := n.Child(i)
				leaves = append(leaves, child)
			}
			return true, nil
		}
		if builtinListCall(n, "concat") {
			left, _ := n.Child(0)
			right, _ := n.Child(1)
			if ok, err := collect(left); err != nil || !ok {
				return false, err
			}
			return collect(right)
		}
		return false, nil
	}
	if ok, err := collect(n); err != nil || !ok {
		return nil, false, err
	}
	var groups []deferredRepeatGroup
	seen := map[string]int{}
	compressed := false
	for position, leaf := range leaves {
		key, bound, err := b.deferredRepeatExpressionKey(leaf)
		if err != nil || key == "" {
			// An extension or callback anywhere in the constructor keeps the
			// whole original path, including its Build invocation behavior.
			return nil, false, err
		}
		if index, exists := seen[key]; bound && exists {
			groups[index].positions = append(groups[index].positions, position)
			compressed = true
			continue
		}
		if bound {
			seen[key] = len(groups)
		}
		// Unbound leaves, including empty-list data and null, retain their
		// own output positions. They are not zero-row outer arms.
		groups = append(groups, deferredRepeatGroup{leaf, []int{position}})
	}
	return groups, compressed, nil
}
