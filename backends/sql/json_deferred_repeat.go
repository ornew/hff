package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// A repeated lexical parameter has one value descriptor and several output
// positions. Share its row scope rather than copying the enclosing FROM for
// each position. Equal types, names, or SQL text are not an identity proof.
func (b *builder) repeatedDeferredList(n hff.Node) (jsonDeferredList, bool, error) {
	first, count, ok, err := b.repeatedDeferredParameter(n)
	if err != nil || !ok || count < 2 {
		return jsonDeferredList{}, false, err
	}
	// Keep the original constructor validation, including every child and
	// lexical lookup, without leaking discarded parameters or projections.
	probe := b.validationBuilder()
	if _, err := probe.deferredListConstructor(n); err != nil {
		return jsonDeferredList{}, true, err
	}
	value, err := b.deferredValue(first)
	if err != nil {
		return jsonDeferredList{}, true, err
	}
	positions := make([]string, count)
	for i := range positions {
		if err := checkContext(b.ctx); err != nil {
			return jsonDeferredList{}, true, err
		}
		positions[i] = "(" + strconv.Itoa(i) + ")"
	}
	alias := b.nextAlias()
	return jsonDeferredList{
		null: "FALSE", present: deferredParameterPresence(value),
		// literal must remain false: a single arm now emits several positions.
		// Literal indexing instead treats each arm as exactly one element.
		arms: []jsonDeferredArm{{
			from:    "(VALUES " + strings.Join(positions, ",") + ") AS " + alias + "(ord)",
			ordinal: []string{alias + ".ord"}, selected: "TRUE", value: value,
			parameters: copyJSONParameters(b.parameters),
		}},
	}, true, nil
}

func (b *builder) repeatedDeferredParameter(n hff.Node) (hff.Node, int, bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return hff.Node{}, 0, false, err
	}
	if builtinListCall(n, "concat") {
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		first, lcount, lok, err := b.repeatedDeferredParameter(left)
		if err != nil || !lok {
			return hff.Node{}, 0, false, err
		}
		second, rcount, rok, err := b.repeatedDeferredParameter(right)
		if err != nil || !rok {
			return hff.Node{}, 0, false, err
		}
		lbind, _ := first.Binding()
		rbind, _ := second.Binding()
		if lbind.ID() != rbind.ID() {
			return hff.Node{}, 0, false, nil
		}
		return first, lcount + rcount, true, nil
	}
	if n.Kind() != hff.ListNode || n.ChildCount() == 0 {
		return hff.Node{}, 0, false, nil
	}
	first, _ := n.Child(0)
	binding, ok := first.Binding()
	if !ok || first.Kind() != hff.ParameterNode {
		return hff.Node{}, 0, false, nil
	}
	if _, bound := b.parameters[binding.ID()]; !bound {
		return hff.Node{}, 0, false, nil
	}
	for i := 1; i < n.ChildCount(); i++ {
		if err := checkContext(b.ctx); err != nil {
			return hff.Node{}, 0, false, err
		}
		child, _ := n.Child(i)
		other, bound := child.Binding()
		if !bound || child.Kind() != hff.ParameterNode || other.ID() != binding.ID() {
			return hff.Node{}, 0, false, nil
		}
	}
	return first, n.ChildCount(), true, nil
}
