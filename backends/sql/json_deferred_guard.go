package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) guardDeferredValue(n hff.Node, value *jsonDeferredValue) (*jsonDeferredValue, error) {
	if value.selection != nil {
		return b.mapDeferredSelection(n, value, func(child *jsonDeferredValue) (*jsonDeferredValue, error) { return b.guardDeferredValue(n, child) })
	}
	if n.Kind() == hff.GuardNode {
		exists, err := b.observeDeferredValue(n, value, "exists")
		if err != nil {
			return nil, err
		}
		copy := *value
		copy.present = exists.code
		return &copy, nil
	}
	target, _ := n.Constraint()
	return b.narrowDeferredValue(n, value, target)
}

func (b *builder) deferredMatches(n hff.Node, value *jsonDeferredValue, target hff.Type) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if value.runtime != nil {
		return b.runtimeMatches(n, value.runtime.code, target)
	}
	if value.selection != nil {
		result, err := b.resolveDeferredSelection(n, value, hff.BoolType, func(child *jsonDeferredValue) (expression, error) {
			test, err := b.deferredMatches(n, child, target)
			return expression{test, child.present, hff.BoolType}, err
		})
		return result.code, err
	}
	if target.Kind() == hff.AnyType {
		return "TRUE", nil
	}
	if target.Kind() == hff.NeverType {
		return "FALSE", nil
	}
	if value.choices != nil {
		tests := make([]string, len(value.choices))
		for i, choice := range value.choices {
			var err error
			tests[i], err = b.deferredMatches(n, choice.value, target)
			if err != nil {
				return "", err
			}
		}
		fallback := "FALSE"
		if acceptsKind(target, hff.NullType) {
			fallback = "TRUE"
		}
		return deferredCases(value.choices, tests, fallback), nil
	}
	if target.Kind() == hff.UnionType {
		var tests []string
		for _, member := range target.Members() {
			test, err := b.deferredMatches(n, value, member)
			if err != nil {
				return "", err
			}
			tests = append(tests, test)
		}
		return "(" + strings.Join(tests, " OR ") + ")", nil
	}
	if value.leaf != nil {
		if value.leaf.itemSlot != nil {
			return b.jsonItemSlotTest(n, *value.leaf.itemSlot, target)
		}
		if jsonHasTaggedString(target) {
			target = jsonNativeCarrierType(target, value.typ)
		}
		if value.leaf.declared && value.leaf.direct == nil && !jsonRootHasKind(value.typ, hff.FloatType) {
			return b.jsonMatches(n, value.leaf.code, value.leaf.format, value.typ, target)
		}
		carrier, err := b.deferredLeafExpression(n, *value.leaf)
		if err != nil {
			return "", err
		}
		raw, format, err := b.jsonRepresentation(n, carrier)
		if err != nil {
			return "", err
		}
		return b.jsonMatches(n, raw, format, value.typ, target)
	}
	if value.list != nil {
		if target.Kind() == hff.NullType {
			return value.list.null, nil
		}
		if target.Kind() != hff.ListType {
			return "FALSE", nil
		}
		var tests []string
		for _, arm := range value.list.arms {
			var test string
			steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
				var err error
				test, err = b.deferredMatches(n, deferredLeaf(arm.value), target.Element())
				return err
			})
			if err != nil {
				return "", err
			}
			if arm.empty {
				continue
			}
			arm.steps = steps
			tests = append(tests, "NOT EXISTS(SELECT 1 FROM "+deferredFrom(arm)+" WHERE "+arm.selected+" AND NOT COALESCE("+test+",FALSE))")
		}
		all := "(NOT " + value.list.null + ")"
		for _, test := range tests {
			all = both(all, test)
		}
		return all, nil
	}
	if value.object != nil {
		if target.Kind() != hff.MapType {
			return "FALSE", nil
		}
		all := "TRUE"
		for _, field := range *value.object {
			test, err := b.deferredMatches(n, field.value, target.Element())
			if err != nil {
				return "", err
			}
			all = both(all, test)
		}
		return all, nil
	}
	return "FALSE", nil
}

// Matching happens before normalization. Recovering a Float or another typed
// leaf may produce ordinary null; subsequent guards must inspect that result.
func (b *builder) narrowDeferredValue(n hff.Node, value *jsonDeferredValue, target hff.Type) (*jsonDeferredValue, error) {
	if value.selection != nil {
		return b.mapDeferredSelection(n, value, func(child *jsonDeferredValue) (*jsonDeferredValue, error) {
			return b.narrowDeferredValue(n, child, target)
		})
	}
	if value.leaf != nil && value.leaf.itemSlot == nil && jsonHasTaggedString(target) {
		// A native JSON Any child has no tagged scalar identity. Bound the
		// guard before normalization, including nested collection targets.
		target = jsonNativeCarrierType(target, value.typ)
	}
	test, err := b.deferredMatches(n, value, target)
	if err != nil {
		return nil, err
	}
	copy := *value
	copy.present = both(value.present, "COALESCE("+test+",FALSE)")
	if value.runtime != nil {
		copy.typ = intersectItemType(value.typ, target)
		if target.Kind() != hff.AnyType {
			// SQL may evaluate projections from a filtered/inactive branch.
			// A failed guard must not expose its payload to a narrowed cast.
			runtime := *value.runtime
			runtime.code = "(CASE WHEN COALESCE(" + test + ",FALSE) THEN " + value.runtime.code + " ELSE " + runtimeNull + " END)"
			copy.runtime = &runtime
		}
		return &copy, nil
	}
	if target.Kind() == hff.AnyType {
		return &copy, nil
	}
	if value.leaf != nil {
		parameter := *value.leaf
		parameter.lossy = parameter.lossy || parameter.format == JSONB && deferredTypeMayFloat(value.typ)
		if parameter.itemSlot != nil {
			slot := *parameter.itemSlot
			slot.constraints = append([]hff.Type(nil), slot.constraints...)
			if err := b.normalizeItemSlotGuard(n, &slot, target); err != nil {
				return nil, err
			}
			slot.constraints = append(slot.constraints, target)
			slot.present = copy.present
			parameter.itemSlot, parameter.format = &slot, slot.format
			parameter.typ = itemSlotGuardType(itemSlotTypes(slot.roles, slot.key != "FALSE", slot.value != "FALSE", slot.constraints), target)
		} else {
			typ := intersectItemType(value.typ, target)
			if typ.Kind() == hff.NeverType {
				return withDeferredPresence(deferredNull(), copy.present), nil
			}
			carrier, err := b.deferredLeafExpression(n, parameter)
			if err != nil {
				return nil, err
			}
			raw, format, err := b.jsonRepresentation(n, carrier)
			if err != nil {
				return nil, err
			}
			raw, err = b.normalizedComparisonDocument(n, raw, format, typ)
			if err != nil {
				return nil, err
			}
			result, err := b.jsonDecode(n, b.project(raw), JSON, typ, copy.present)
			if err != nil {
				return nil, err
			}
			parameter.typ, parameter.direct, parameter.format = typ, &result, JSON
		}
		copy.typ, copy.leaf = parameter.typ, &parameter
		return &copy, nil
	}
	if value.choices != nil {
		copy.choices = append([]jsonDeferredChoice(nil), value.choices...)
		for i := range copy.choices {
			copy.choices[i].value, err = b.narrowDeferredValue(n, copy.choices[i].value, target)
			if err != nil {
				return nil, err
			}
		}
		copy.typ = deferredChoiceType(copy.choices)
		return &copy, nil
	}
	if value.list != nil {
		element := itemGuardElement(target)
		plan, err := b.deferredValueList(n, value)
		if err != nil {
			return nil, err
		}
		var types []hff.Type
		for i, arm := range plan.arms {
			var child *jsonDeferredValue
			if plan.literal && len(arm.steps) == 0 && !arm.empty {
				// A literal's children belong to this lexical scope. Keep their
				// normalization projections here: direct indexing can select a
				// child without entering a generated row scope.
				child, err = b.narrowDeferredValue(n, deferredLeaf(arm.value), element)
			} else {
				var steps []string
				var parameters map[hff.BindingID]jsonParameter
				steps, parameters, err = b.inDeferredArm(arm, hff.Node{}, func() error {
					var err error
					child, err = b.narrowDeferredValue(n, deferredLeaf(arm.value), element)
					return err
				})
				plan.arms[i].steps, plan.arms[i].parameters = steps, parameters
			}
			if err != nil {
				return nil, err
			}
			plan.arms[i].value = deferredValueParameter(child)
			if !arm.empty {
				types = append(types, child.typ)
			}
		}
		copy.list, copy.typ = &plan, hff.NewListType(hff.NewUnionType(types...))
		return &copy, nil
	}
	if value.object != nil {
		element := deferredObjectGuardElement(target)
		fields := append([]jsonDeferredField(nil), (*value.object)...)
		var types []hff.Field
		for i := range fields {
			fields[i].value, err = b.narrowDeferredValue(n, fields[i].value, element)
			if err != nil {
				return nil, err
			}
			types = append(types, hff.Field{Name: fields[i].name, Type: fields[i].value.typ})
		}
		copy.object, copy.typ = &fields, hff.NewStructType(types...)
	}
	return &copy, nil
}

func deferredObjectGuardElement(target hff.Type) hff.Type {
	if target.Kind() == hff.AnyType {
		return target
	}
	if target.Kind() == hff.MapType {
		return target.Element()
	}
	var types []hff.Type
	for _, member := range target.Members() {
		types = append(types, deferredObjectGuardElement(member))
	}
	return hff.NewUnionType(types...)
}
