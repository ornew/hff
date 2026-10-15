package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) deferredFindDescriptor(n hff.Node) (*jsonDeferredValue, error) {
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	receiver, _ := n.Child(0)
	plan, err := probe.deferredList(receiver)
	if err != nil {
		return nil, err
	}
	// A successful ordinary lowering is insufficient when Any absorbed a
	// per-key tagged alternative. Prove identity from the original row plan.
	safe, err := probe.deferredInputIdentity(&jsonDeferredValue{typ: b.jsonStoredType(receiver), present: plan.present, list: &plan})
	if err != nil {
		return nil, err
	}
	if safe {
		normal := b.validationBuilder()
		normal.keepDeferredValues = false
		result, err := normal.call(n)
		if err == nil {
			b.adoptDeferredProbe(normal)
			return deferredLeaf(jsonParameter{typ: b.jsonStoredType(n), direct: &result, format: b.jsonValues[result.code],
				lossy: b.losesFloat(n, nil), itemRoles: b.jsonItemRoles(n), itemListRoles: b.jsonItemsRoles(n)}), nil
		}
		if err := b.typedFallbackError(err); err != nil {
			return nil, err
		}
	}
	value, err := probe.findDeferredValue(n, plan)
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return value, err
}

// Select only an arm and its global ordinal. The original value descriptor
// stays in its lexical scope, including nested constructors and tagged values.
func (b *builder) findDeferredValue(n hff.Node, plan jsonDeferredList) (*jsonDeferredValue, error) {
	if len(plan.arms) == 0 {
		// Validate the callback even for a known null/non-list receiver. This
		// zero-row arm must not change the receiver's null or presence state.
		alias := b.nextAlias()
		null := expression{"NULL", "TRUE", hff.NullType}
		plan.arms = []jsonDeferredArm{{empty: true, from: "(SELECT 1 WHERE FALSE) AS " + alias,
			ordinal: []string{"0"}, selected: "TRUE", value: jsonParameter{typ: hff.PrimitiveType(hff.NeverType), direct: &null}, parameters: copyJSONParameters(b.parameters)}}
	}
	callback, _ := n.Child(1)
	body, _ := callback.Child(0)
	selection := &jsonDeferredSelection{arms: append([]jsonDeferredArm(nil), plan.arms...), fallback: deferredNull()}
	var rows []string
	for i, arm := range plan.arms {
		selected := arm.selected
		steps, parameters, err := b.inDeferredArm(arm, callback, func() error {
			predicate, err := b.deferredPredicate(body)
			if err != nil {
				return err
			}
			selected = both(selected, b.truth(body, predicate))
			return nil
		})
		if err != nil {
			return nil, err
		}
		arm.steps, arm.parameters, arm.selected = steps, parameters, selected
		selection.arms[i] = arm
		rows = append(rows, "SELECT "+strconv.Itoa(i)+" AS arm,"+deferredOrdinal(arm)+" AS ord FROM "+deferredFrom(arm)+" WHERE "+selected)
	}
	source, seed, picked, chosen := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	active := both(plan.present, "(NOT "+plan.null+")")
	selectRow := "WITH " + source + " AS (" + strings.Join(rows, " UNION ALL ") + ") SELECT arm,ord FROM " + source + " WHERE " + active + " ORDER BY ord LIMIT 1"
	b.steps = append(b.steps, "(SELECT "+picked+".arm,"+picked+".ord FROM (VALUES (1)) AS "+seed+"(n) LEFT JOIN LATERAL ("+selectRow+") AS "+picked+" ON TRUE OFFSET 0) AS "+chosen)
	for i := range selection.arms {
		arm := &selection.arms[i]
		arm.selected = both(arm.selected, "("+chosen+".arm="+strconv.Itoa(i)+" AND "+chosen+".ord="+deferredOrdinal(*arm)+")")
	}
	return b.finishDeferredSelection(n, selection, plan.present)
}
