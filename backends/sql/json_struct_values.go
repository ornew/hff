package sql

import (
	"sort"
	"strconv"

	"github.com/ornew/hff"
)

// Values from a single declared Struct retain their originating field rather
// than acquiring the union of every field's storage representations.
func (b *builder) structValuesType(n hff.Node) (hff.Type, bool, error) {
	method, ok := n.Method()
	if !ok || method.Name() != "values" || n.ChildCount() != 1 {
		return hff.Type{}, false, nil
	}
	for _, signature := range method.Signatures() {
		if signature.ID() != "object.values" {
			return hff.Type{}, false, nil
		}
	}
	receiver, _ := n.Child(0)
	typ, known := deferredStructType(b.jsonStoredType(receiver))
	if !known {
		return hff.Type{}, false, nil
	}
	needed, err := b.deferredStructAlternatives(typ)
	return typ, needed, err
}

func (b *builder) declaredStructReceiver(receiver hff.Node) (*jsonDeferredValue, error) {
	value, handled, err := b.deferredDescriptor(receiver)
	if handled || err != nil {
		return value, err
	}
	if receiver.Kind() == hff.ObjectNode {
		return b.constructDeferredValue(receiver)
	}
	carrier, err := b.expression(receiver)
	if err != nil {
		return nil, err
	}
	return deferredLeaf(jsonParameter{typ: b.jsonStoredType(receiver), direct: &carrier, format: b.jsonValues[carrier.code], lossy: b.losesFloat(receiver, nil)}), nil
}

func (b *builder) deferredStructValues(n hff.Node) (*jsonDeferredValue, bool, error) {
	_, needed, err := b.structValuesType(n)
	if !needed || err != nil {
		return nil, err != nil, err
	}
	probe := b.validationBuilder()
	receiver, _ := n.Child(0)
	value, err := probe.declaredStructReceiver(receiver)
	if err != nil {
		return nil, true, err
	}
	result, err := probe.structValuesDescriptor(n, value)
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return result, true, err
}

func (b *builder) structValuesDescriptor(n hff.Node, value *jsonDeferredValue) (*jsonDeferredValue, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, err
	}
	if value.selection != nil {
		return b.mapDeferredSelection(n, value, func(child *jsonDeferredValue) (*jsonDeferredValue, error) { return b.structValuesDescriptor(n, child) })
	}
	if value.choices != nil {
		choices := make([]jsonDeferredChoice, 0, len(value.choices))
		for _, choice := range value.choices {
			child, err := b.structValuesDescriptor(n, choice.value)
			if err != nil {
				return nil, err
			}
			choices = append(choices, jsonDeferredChoice{choice.when, child})
		}
		// A failed selection denotes ordinary null, whose values() result is null.
		return makeDeferredChoices(choices, value.present), nil
	}
	plan := jsonDeferredList{null: "FALSE", present: value.present}
	if value.object != nil {
		fields := append([]jsonDeferredField(nil), (*value.object)...)
		sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })
		for i, field := range fields {
			if err := checkContext(b.ctx); err != nil {
				return nil, err
			}
			seed := b.nextAlias()
			plan.arms = append(plan.arms, jsonDeferredArm{from: "(VALUES (1)) AS " + seed + "(n)", ordinal: []string{strconv.Itoa(i)}, selected: "TRUE", value: jsonParameter{typ: field.value.typ, deferred: field.value, lossy: deferredValueLoss(field.value)}, parameters: copyJSONParameters(b.parameters)})
		}
	} else {
		typ, known := deferredStructType(value.typ)
		if !known {
			plan.null = "TRUE"
			return &jsonDeferredValue{typ: n.Type(), present: plan.present, list: &plan}, nil
		}
		var carrier expression
		var err error
		if value.leaf != nil {
			carrier, err = b.deferredLeafExpression(n, *value.leaf)
		} else {
			carrier, err = b.materializeDeferredValue(n, value)
		}
		if err != nil {
			return nil, err
		}
		raw, format, err := b.jsonRepresentation(n, carrier)
		if err != nil {
			return nil, err
		}
		object := "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='object' THEN " + raw + " ELSE NULL::" + string(format) + " END)"
		plan.null = "(" + object + " IS NULL)"
		item, rows := b.nextAlias(), b.nextAlias()
		from := "(SELECT " + item + ".key," + item + ".value,row_number() OVER(ORDER BY " + item + ".key COLLATE \"C\") AS ord FROM " + string(format) + "_each(" + object + ") AS " + item + "(key,value)) AS " + rows
		var choices []jsonDeferredChoice
		for _, field := range typ.Fields() {
			if err := checkContext(b.ctx); err != nil {
				return nil, err
			}
			key, err := b.parameter(n, hff.String(field.Name))
			if err != nil {
				return nil, err
			}
			// Shape-only consumers can omit every field choice. Retain a typed
			// parameter projection even when no emitted value is inspected.
			key.code = b.project(key.code + "::text")
			leaf := deferredLeaf(jsonParameter{code: rows + ".value", format: format, typ: field.Type, declared: true, lossy: deferredValueLoss(value) && jsonHasFloat(field.Type)})
			choices = append(choices, jsonDeferredChoice{"(" + rows + ".key COLLATE \"C\"=" + key.code + " COLLATE \"C\")", leaf})
		}
		selected := makeDeferredChoices(choices, "TRUE")
		plan.arms = []jsonDeferredArm{{from: from, ordinal: []string{rows + ".ord"}, selected: "TRUE", value: deferredValueParameter(selected), parameters: copyJSONParameters(b.parameters)}}
	}
	return &jsonDeferredValue{typ: n.Type(), present: plan.present, list: &plan}, nil
}
