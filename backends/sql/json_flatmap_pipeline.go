package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// A list plan keeps values in lexical row scopes instead of serializing an
// ambiguous intermediate list. List state is separate from emitted rows, so
// an empty callback result cannot hide its parent's no-value failure.
type jsonDeferredList struct {
	null, present string
	arms          []jsonDeferredArm
	literal       bool
}

type jsonDeferredArm struct {
	empty      bool
	from       string
	ordinal    []string
	selected   string
	value      jsonParameter
	steps      []string
	parameters map[hff.BindingID]jsonParameter
}

func builtinListCall(n hff.Node, name string) bool {
	method, ok := n.Method()
	if !ok || method.Name() != name {
		return false
	}
	for _, signature := range method.Signatures() {
		if signature.ID() != "list."+name {
			return false
		}
	}
	return true
}

// Preserve established key-only and whole-pair lowering. Enter the row-plan
// path only when a flatmap can carry a selected value slot or an existing
// deferred parameter. Callback binding IDs prevent captures from being
// mistaken for a newly bound item with the same spelling.
func (b *builder) needsDeferredFlatmap(n hff.Node) (bool, error) {
	flatmap, slot := false, false
	var visit func(hff.Node) error
	visit = func(node hff.Node) error {
		if err := checkContext(b.ctx); err != nil {
			return err
		}
		if _, needed, err := b.structValuesType(node); err != nil {
			return err
		} else if needed {
			flatmap, slot = true, true
		}
		flatmap = flatmap || builtinListCall(node, "flatmap")
		if builtinListCall(node, "map") {
			callback, _ := node.Child(1)
			body, _ := callback.Child(0)
			bindings, _ := callback.Parameters()
			constructed, err := b.callbackConstructsParameter(body, bindings)
			if err != nil {
				return err
			}
			flatmap = flatmap || constructed
		}
		if binding, ok := node.Binding(); ok && b.parameters[binding.ID()].itemSlot != nil {
			slot = true
		}
		if binding, ok := node.Binding(); ok && b.parameters[binding.ID()].deferred != nil {
			flatmap, slot = true, true
		}
		if node.Kind() == hff.IndexNode {
			parent, _ := node.Child(0)
			index, _ := node.Child(1)
			x, fixed := itemLiteralIndex(index)
			if (!fixed || x == 1 || x == -1) && b.jsonItemRoles(parent) != nil {
				slot = true
			}
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if parameters, ok := child.Parameters(); ok && len(parameters) == 1 {
				receiver, _ := node.Child(0)
				item := jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true)
				restore := b.jsonCallbackParameters(child, []hff.Type{item}, []*jsonItemRoles{b.jsonItemsRoles(receiver)})
				err := visit(child)
				restore()
				if err != nil {
					return err
				}
			} else {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := visit(n)
	return flatmap && slot, err
}

func (b *builder) callbackConstructsParameter(n hff.Node, parameters []hff.Binding) (bool, error) {
	var visit func(hff.Node, bool) (bool, error)
	visit = func(node hff.Node, inside bool) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		inside = inside || node.Kind() == hff.ListNode || node.Kind() == hff.ObjectNode
		if binding, ok := node.Binding(); ok && inside {
			for _, parameter := range parameters {
				if binding.ID() == parameter.ID() {
					return true, nil
				}
			}
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if found, err := visit(child, inside); found || err != nil {
				return found, err
			}
		}
		return false, nil
	}
	return visit(n, false)
}

func deferredParameterPresence(value jsonParameter) string {
	if value.deferred != nil {
		return value.deferred.present
	}
	if value.itemSlot != nil {
		return value.itemSlot.present
	}
	if value.direct != nil {
		return value.direct.present
	}
	return "TRUE"
}

func deferredFrom(arm jsonDeferredArm) string {
	from := arm.from
	if len(arm.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(arm.steps, " CROSS JOIN LATERAL ")
	}
	return from
}

func deferredOrdinal(arm jsonDeferredArm) string {
	return "ARRAY[" + strings.Join(arm.ordinal, ",") + "]::bigint[]"
}

func copyJSONParameters(parameters map[hff.BindingID]jsonParameter) map[hff.BindingID]jsonParameter {
	result := make(map[hff.BindingID]jsonParameter, len(parameters))
	for id, parameter := range parameters {
		result[id] = parameter
	}
	return result
}

func (b *builder) inDeferredArm(arm jsonDeferredArm, callback hff.Node, work func() error) ([]string, map[hff.BindingID]jsonParameter, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, nil, err
	}
	outer, paths, parameters, memo := b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo
	b.steps, b.sharedPaths, b.itemRoleMemo = append([]string(nil), arm.steps...), nil, nil
	b.parameters = copyJSONParameters(arm.parameters)
	defer func() { b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo = outer, paths, parameters, memo }()
	if callback.Valid() {
		bindings, ok := callback.Parameters()
		if !ok || len(bindings) != 1 {
			return nil, nil, b.failure(callback, "list pipeline requires one callback parameter")
		}
		b.parameters[bindings[0].ID()] = arm.value
	}
	if err := work(); err != nil {
		return nil, nil, err
	}
	return append([]string(nil), b.steps...), copyJSONParameters(b.parameters), nil
}

func (b *builder) deferredValue(n hff.Node) (jsonParameter, error) {
	if n.Kind() == hff.ListNode || n.Kind() == hff.ObjectNode {
		value, err := b.constructDeferredValue(n)
		return deferredValueParameter(value), err
	}
	if value, handled, err := b.deferredDescriptor(n); handled || err != nil {
		return deferredValueParameter(value), err
	}
	if builtinListCall(n, "size") || builtinListCall(n, "count") && n.ChildCount() == 1 {
		receiver, _ := n.Child(0)
		plan, err := b.deferredList(receiver)
		if err != nil {
			return jsonParameter{}, err
		}
		var counts []string
		for _, arm := range plan.arms {
			counts = append(counts, "(SELECT count(*) FROM "+deferredFrom(arm)+" WHERE "+arm.selected+")")
		}
		count := "0::bigint"
		if len(counts) == 1 {
			count = counts[0]
		} else if len(counts) > 1 {
			// Each count fits bigint, but their sum need not. Keep overflow
			// an ordinary evaluation failure rather than a database error.
			for i := range counts {
				counts[i] += "::numeric"
			}
			total := b.project("(" + strings.Join(counts, "+") + ")")
			count = "(CASE WHEN " + total + "<='9223372036854775807'::numeric THEN " + total + "::bigint ELSE NULL::bigint END)"
		}
		value := expression{code: "(CASE WHEN " + plan.null + " THEN NULL::bigint ELSE " + count + " END)", present: plan.present, kind: hff.IntType}
		return jsonParameter{typ: hff.PrimitiveType(hff.IntType), direct: &value}, nil
	}
	slot, handled, err := b.jsonItemSlot(n)
	if err != nil {
		return jsonParameter{}, err
	}
	stored := b.jsonStoredType(n)
	if handled {
		return jsonParameter{typ: stored, format: slot.format, lossy: b.losesFloat(n, nil), itemSlot: &slot}, nil
	}
	value, err := b.expression(n)
	if err != nil {
		return jsonParameter{}, err
	}
	if _, structured := b.jsonValues[value.code]; structured {
		raw, err := b.computedJSONDocument(n, value, stored)
		if err != nil {
			return jsonParameter{}, err
		}
		raw = b.project(raw)
		b.rememberNormalizedJSON(raw, JSON, stored)
		value, err = b.jsonDecodeProjected(n, raw, JSON, stored, value.present)
		if err != nil {
			return jsonParameter{}, err
		}
	} else if (n.Kind() == hff.LiteralNode || b.literalConversions[n].handled) && value.kind != hff.NullType {
		// Constructor children can later be inspected only for their family.
		// Keep a bound literal named in this scope even if a subsequent
		// inapplicable operation returns null without reading its payload.
		value.code = b.project(value.code)
	}
	return jsonParameter{typ: stored, direct: &value, format: b.jsonValues[value.code], lossy: b.losesFloat(n, nil), itemRoles: b.jsonItemRoles(n), itemListRoles: b.jsonItemsRoles(n)}, nil
}

func (b *builder) deferredList(n hff.Node) (jsonDeferredList, error) {
	if err := checkContext(b.ctx); err != nil {
		return jsonDeferredList{}, err
	}
	if n.Kind() == hff.ListNode {
		plan := jsonDeferredList{null: "FALSE", present: "TRUE", literal: true}
		if n.ChildCount() == 0 {
			// Keep a typed, zero-row scope so subsequent callbacks still pass
			// through ordinary Build validation. Its output type cannot occur
			// and must not make another arm's value identity ambiguous.
			alias := b.nextAlias()
			value := expression{code: "NULL", present: "TRUE", kind: hff.NullType}
			plan.arms = append(plan.arms, jsonDeferredArm{empty: true, from: "(SELECT 1 AS n WHERE FALSE) AS " + alias,
				ordinal: []string{"0"}, selected: "TRUE", value: jsonParameter{typ: hff.PrimitiveType(hff.NeverType), direct: &value}, parameters: copyJSONParameters(b.parameters)})
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			value, err := b.deferredValue(child)
			if err != nil {
				return jsonDeferredList{}, err
			}
			plan.present = both(plan.present, deferredParameterPresence(value))
			seed := b.nextAlias()
			plan.arms = append(plan.arms, jsonDeferredArm{from: "(VALUES (1)) AS " + seed + "(n)", ordinal: []string{strconv.Itoa(i)}, selected: "TRUE", value: value, parameters: copyJSONParameters(b.parameters)})
		}
		return plan, nil
	}
	if builtinListCall(n, "concat") {
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		l, err := b.deferredList(left)
		if err != nil {
			return jsonDeferredList{}, err
		}
		r, err := b.deferredList(right)
		if err != nil {
			return jsonDeferredList{}, err
		}
		for i := range l.arms {
			if err := checkContext(b.ctx); err != nil {
				return jsonDeferredList{}, err
			}
			l.arms[i].ordinal = append([]string{"0"}, l.arms[i].ordinal...)
		}
		for i := range r.arms {
			if err := checkContext(b.ctx); err != nil {
				return jsonDeferredList{}, err
			}
			r.arms[i].ordinal = append([]string{"1"}, r.arms[i].ordinal...)
		}
		return jsonDeferredList{null: "(" + l.null + " OR " + r.null + ")", present: both(l.present, "("+l.null+" OR "+r.present+")"), arms: append(l.arms, r.arms...), literal: l.literal && r.literal}, nil
	}
	for _, name := range []string{"map", "filter", "flatmap"} {
		if builtinListCall(n, name) {
			return b.deferredListTransform(n, name)
		}
	}
	if value, handled, err := b.deferredDescriptor(n); handled || err != nil {
		if err != nil {
			return jsonDeferredList{}, err
		}
		return b.deferredValueList(n, value)
	}
	if slot, handled, err := b.jsonItemSlot(n); handled || err != nil {
		if err != nil {
			return jsonDeferredList{}, err
		}
		return b.deferredSlotList(n, slot)
	}
	value, err := b.expression(n)
	if err != nil {
		return jsonDeferredList{}, err
	}
	if value.kind != hff.ListType && value.kind != hff.AnyType {
		return jsonDeferredList{null: "TRUE", present: value.present}, nil
	}
	value, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return jsonDeferredList{}, err
	}
	alias := b.nextAlias()
	return jsonDeferredList{null: "(" + value.code + " IS NULL)", present: value.present, arms: []jsonDeferredArm{{
		from: string(format) + "_array_elements(" + value.code + ") WITH ORDINALITY AS " + alias + "(value,ord)", ordinal: []string{alias + ".ord"}, selected: "TRUE",
		value: jsonParameter{code: alias + ".value", format: format, typ: jsonStoredMemberType(sortListType(b.jsonStoredType(n)), "", false, true), lossy: b.losesFloat(n, nil), itemRoles: b.jsonItemsRoles(n)}, parameters: copyJSONParameters(b.parameters),
	}}}, nil
}

// A source-key branch can have a different list element schema from another
// branch. Dispatch from that schema proof, never from a stored string's shape.
func (b *builder) deferredSlotList(n hff.Node, slot jsonItemSlot) (jsonDeferredList, error) {
	plan := jsonDeferredList{null: "TRUE", present: slot.present}
	var seen []hff.Type
	for _, typ := range itemSlotTypes(slot.roles, slot.key != "FALSE", slot.value != "FALSE", slot.constraints) {
		list := nonnullJSONType(containsListType(typ))
		if list.Kind() != hff.ListType && list.Kind() != hff.AnyType {
			continue
		}
		duplicate := false
		for _, previous := range seen {
			duplicate = duplicate || previous.Equal(typ)
		}
		if duplicate {
			continue
		}
		seen = append(seen, typ)
		raw, err := b.jsonItemSlotOperation(n, slot, "NULL::json", func(source hff.Type) (string, error) {
			if !source.Equal(typ) {
				return "NULL::json", nil
			}
			normalized, err := b.normalizedComparisonDocument(n, slot.raw, slot.format, containsListType(source))
			return "(CASE WHEN " + string(slot.format) + "_typeof(" + slot.raw + ")='array' THEN " + normalized + " ELSE NULL::json END)", err
		})
		if err != nil {
			return jsonDeferredList{}, err
		}
		raw = b.project(raw)
		plan.null = both(plan.null, "("+raw+" IS NULL)")
		alias := b.nextAlias()
		plan.arms = append(plan.arms, jsonDeferredArm{from: "json_array_elements(" + raw + ") WITH ORDINALITY AS " + alias + "(value,ord)", ordinal: []string{alias + ".ord"}, selected: "TRUE", value: jsonParameter{code: alias + ".value", format: JSON, typ: jsonStoredMemberType(list, "", false, true), lossy: b.losesFloat(n, nil)}, parameters: copyJSONParameters(b.parameters)})
	}
	return plan, nil
}

func (b *builder) deferredListTransform(n hff.Node, name string) (jsonDeferredList, error) {
	receiver, _ := n.Child(0)
	plan, err := b.deferredList(receiver)
	if err != nil {
		return jsonDeferredList{}, err
	}
	callback, _ := n.Child(1)
	body, _ := callback.Child(0)
	output := jsonDeferredList{null: plan.null, present: plan.present}
	var sentinels []string
	for _, arm := range plan.arms {
		var child jsonDeferredList
		value, selected := arm.value, arm.selected
		steps, parameters, err := b.inDeferredArm(arm, callback, func() error {
			if name == "flatmap" {
				child, err = b.deferredList(body)
				return err
			}
			if name == "filter" {
				predicate, err := b.deferredPredicate(body)
				if err != nil {
					return err
				}
				selected = both(selected, b.truth(body, predicate))
				return nil
			}
			value, err = b.deferredValue(body)
			return err
		})
		if err != nil {
			return jsonDeferredList{}, err
		}
		prepared := arm
		prepared.steps, prepared.parameters = steps, parameters
		if name == "flatmap" {
			sentinels = append(sentinels, "SELECT "+deferredOrdinal(arm)+" AS ord,"+child.present+" AS p,"+child.null+" AS n FROM "+deferredFrom(prepared)+" WHERE "+arm.selected)
			if arm.empty && len(child.arms) == 0 {
				// The callback is never invoked, so even a non-list callback
				// result leaves an empty list. Preserve a validation scope for
				// later callbacks rather than dropping the last zero-row arm.
				value := expression{code: "NULL", present: "TRUE", kind: hff.NullType}
				prepared.value = jsonParameter{typ: hff.PrimitiveType(hff.NeverType), direct: &value}
				output.arms = append(output.arms, prepared)
			}
			for _, nested := range child.arms {
				if err := checkContext(b.ctx); err != nil {
					return jsonDeferredList{}, err
				}
				nested.empty = arm.empty || nested.empty
				nested.from = deferredFrom(prepared) + " CROSS JOIN LATERAL " + nested.from
				nested.ordinal = append(append([]string(nil), arm.ordinal...), nested.ordinal...)
				nested.selected = both(arm.selected, nested.selected)
				output.arms = append(output.arms, nested)
			}
		} else {
			prepared.value, prepared.selected = value, selected
			output.arms = append(output.arms, prepared)
			if name == "map" {
				sentinels = append(sentinels, "SELECT "+pipelineRowPresence(arm.selected, deferredParameterPresence(value))+" AS p FROM "+deferredFrom(prepared))
			}
		}
	}
	if len(sentinels) == 0 {
		return output, nil
	}
	rows, state := b.nextAlias(), b.nextAlias()
	if name == "map" {
		p := "(SELECT COALESCE(bool_and(COALESCE(p,FALSE)),TRUE) FROM " + rows + ")"
		b.steps = append(b.steps, "(WITH "+rows+" AS MATERIALIZED ("+strings.Join(sentinels, " UNION ALL ")+") SELECT "+both(plan.present, "("+plan.null+" OR "+p+")")+" AS p OFFSET 0) AS "+state)
		output.present = state + ".p"
	} else {
		first := b.nextAlias()
		cte := "WITH " + rows + " AS MATERIALIZED (" + strings.Join(sentinels, " UNION ALL ") + ")," + first + " AS MATERIALIZED (SELECT p FROM " + rows + " WHERE NOT COALESCE(p,FALSE) OR n ORDER BY ord LIMIT 1)"
		null := "(" + plan.null + " OR EXISTS(SELECT 1 FROM " + first + "))"
		present := both(plan.present, "("+plan.null+" OR COALESCE((SELECT p FROM "+first+"),TRUE))")
		b.steps = append(b.steps, "("+cte+" SELECT "+null+" AS n,"+present+" AS p OFFSET 0) AS "+state)
		output.null, output.present = state+".n", state+".p"
	}
	return output, nil
}

func (b *builder) jsonFlatmapPipeline(n hff.Node, name string) (expression, bool, error) {
	receiver, _ := n.Child(0)
	if b.jsonItemsRoles(receiver) != nil {
		return expression{}, false, nil
	}
	needed, err := b.needsDeferredFlatmap(receiver)
	if err == nil && !needed {
		// Struct selections retain field identity in row plans even without
		// a flatmap or an already-bound deferred callback parameter.
		needed, err = b.hasDeferredStructSource(receiver)
	}
	if err != nil || !needed {
		return expression{}, needed || err != nil, err
	}
	plan, err := b.deferredList(receiver)
	if err != nil {
		return expression{}, true, err
	}
	callback, _ := n.Child(1)
	body, _ := callback.Child(0)
	var rows []string
	var types []hff.Type
	for _, arm := range plan.arms {
		var raw, present string
		steps, _, err := b.inDeferredArm(arm, callback, func() error {
			if name != "map" {
				value, err := b.deferredPredicate(body)
				if err != nil {
					return err
				}
				raw, present = b.truth(body, value), value.present
				return nil
			}
			value, err := b.expression(body)
			if err != nil {
				return err
			}
			present = value.present
			if name == "map" {
				stored := b.jsonStoredType(body)
				if !arm.empty {
					types = append(types, stored)
				}
				raw, err = b.computedJSONDocument(body, value, stored)
			} else {
				raw = b.truth(body, value)
			}
			return err
		})
		if err != nil {
			return expression{}, true, err
		}
		arm.steps = steps
		rows = append(rows, "SELECT "+deferredOrdinal(arm)+" AS ord,"+raw+" AS v,"+pipelineRowPresence(arm.selected, present)+" AS p,"+arm.selected+" AS keep FROM "+deferredFrom(arm))
	}
	value, err := b.finishDeferredList(n, name, plan, rows, types)
	return value, true, err
}

func (b *builder) finishDeferredList(n hff.Node, name string, plan jsonDeferredList, rows []string, types []hff.Type) (expression, error) {
	if len(rows) == 0 {
		carrier := "NULL::boolean"
		if name == "map" {
			carrier = "NULL::json"
		}
		rows = append(rows, "SELECT '{}'::bigint[] AS ord,"+carrier+" AS v,TRUE AS p,TRUE AS keep WHERE FALSE")
	}
	joined, output := b.nextAlias(), b.nextAlias()
	var aggregate string
	kind, present := hff.BoolType, plan.present
	switch name {
	case "map":
		kind = hff.ListType
		if b.jsonItemsRoles(n) == nil {
			for i, left := range types {
				if err := checkContext(b.ctx); err != nil {
					return expression{}, err
				}
				for _, right := range types[i+1:] {
					if err := checkContext(b.ctx); err != nil {
						return expression{}, err
					}
					if jsonTaggedStringStateAmbiguous(left, right) {
						return expression{}, b.failure(n, "flatmap pipeline map results require recoverable element types")
					}
				}
			}
		}
		aggregate = "(SELECT COALESCE(json_agg(v ORDER BY ord),'[]'::json) FROM " + joined + " WHERE keep)"
		present = both(present, "("+plan.null+" OR (SELECT COALESCE(bool_and(COALESCE(p,FALSE)),TRUE) FROM "+joined+"))")
	case "any":
		aggregate = "(SELECT COALESCE(bool_or(v),FALSE) FROM " + joined + " WHERE keep)"
	case "all":
		aggregate = "(SELECT COALESCE(bool_and(v),TRUE) FROM " + joined + " WHERE keep)"
	case "count":
		kind = hff.IntType
		aggregate = "(SELECT count(*) FROM " + joined + " WHERE keep AND v)"
	default:
		return expression{}, b.failure(n, "unsupported flatmap pipeline consumer")
	}
	code := "(CASE WHEN " + plan.null + " THEN NULL ELSE " + aggregate + " END)"
	b.steps = append(b.steps, "(WITH "+joined+" AS MATERIALIZED ("+strings.Join(rows, " UNION ALL ")+") SELECT "+code+" AS v,"+present+" AS p OFFSET 0) AS "+output)
	if name == "map" {
		b.jsonValues[output+".v"] = JSON
		b.rememberNormalizedJSON(output+".v", JSON, hff.NewListType(hff.NewUnionType(types...)))
	}
	return expression{output + ".v", output + ".p", kind}, nil
}

// Materialization is allowed only after every emitted value has an ordinary,
// recoverable representation. The private row plan never escapes as JSON.
func (b *builder) materializeDeferredFlatmap(n hff.Node) (expression, bool, error) {
	if b.jsonItemsRoles(n) != nil {
		return expression{}, false, nil
	}
	needed, err := b.needsDeferredFlatmap(n)
	if err != nil || !needed {
		return expression{}, needed || err != nil, err
	}
	plan, err := b.deferredList(n)
	if err != nil {
		return expression{}, true, err
	}
	value, err := b.materializeDeferredList(n, plan)
	return value, true, err
}

func (b *builder) materializeDeferredList(n hff.Node, plan jsonDeferredList) (expression, error) {
	var rows []string
	var types []hff.Type
	for _, arm := range plan.arms {
		var raw, present string
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			parameter := arm.value
			var value expression
			var err error
			if parameter.deferred != nil {
				value, err = b.materializeDeferredValue(n, parameter.deferred)
			} else if parameter.itemSlot != nil {
				value, err = b.jsonItemSlotValue(n, *parameter.itemSlot)
			} else if parameter.direct != nil {
				value = *parameter.direct
			} else {
				value, err = b.jsonDecode(n, parameter.code, parameter.format, parameter.typ, "TRUE")
			}
			if err != nil {
				return err
			}
			present = value.present
			if !arm.empty {
				types = append(types, parameter.typ)
			}
			raw, err = b.computedJSONDocument(n, value, parameter.typ)
			return err
		})
		if err != nil {
			return expression{}, err
		}
		arm.steps = steps
		rows = append(rows, "SELECT "+deferredOrdinal(arm)+" AS ord,"+raw+" AS v,"+pipelineRowPresence(arm.selected, present)+" AS p,"+arm.selected+" AS keep FROM "+deferredFrom(arm))
	}
	value, err := b.finishDeferredList(n, "map", plan, rows, types)
	return value, err
}
