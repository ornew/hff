package sql

import "github.com/ornew/hff"

// The private tree retains actual tags even when ordinary JSON cannot encode
// the same union. Reuse only positional analysis in an isolated lexical scope;
// these results must never become raw item roles or normalization proofs.
func (b *builder) runtimeItemProvenance(n hff.Node) (jsonItemProvenance, error) {
	probe := *b
	probe.parameters = copyJSONParameters(b.parameters)
	for id, parameter := range probe.parameters {
		if parameter.deferred != nil && parameter.deferred.runtime != nil {
			parameter.itemRoles = parameter.deferred.runtime.itemRoles
			parameter.itemListRoles = parameter.deferred.runtime.itemListRoles
			probe.parameters[id] = parameter
		}
	}
	probe.itemRoleMemo = nil
	probe.runtimeRoleAnalysis = true
	proof := jsonItemProvenance{pair: probe.jsonItemRoles(n), list: probe.jsonItemsRoles(n)}
	return proof, checkContext(b.ctx)
}

func objectEnumeration(n hff.Node) (string, bool) {
	method, ok := n.Method()
	if !ok || n.ChildCount() != 1 {
		return "", false
	}
	name := method.Name()
	if name != "keys" && name != "values" && name != "items" {
		return "", false
	}
	for _, signature := range method.Signatures() {
		if signature.ID() != "object."+name {
			return "", false
		}
	}
	return name, true
}

// This gate selects consumers that may need a private carrier; it does not
// change positional type proofs or assert that ordinary JSON has runtime tags.
// Keep the analysis structural, rather than recursively lowering descriptors.
func (b *builder) needsRuntimeObjectEnumeration(n hff.Node) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if _, enumeration := objectEnumeration(n); enumeration {
		receiver, _ := n.Child(0)
		possible, err := b.hasDeferredValueSource(receiver)
		if err != nil {
			return false, err
		}
		if possible {
			typ := b.jsonStoredType(receiver)
			if !runtimeSchemaRecoverable(typ) {
				return true, nil
			}
			if err := b.checkJSONStringSchemas(n, typ); err != nil {
				if fatal := b.typedFallbackError(err); fatal != nil {
					return false, fatal
				}
				return true, nil
			}
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if needed, err := b.needsRuntimeObjectEnumeration(child); needed || err != nil {
			return needed, err
		}
	}
	return false, nil
}

func (b *builder) hasRuntimeObjectValue(value *jsonDeferredValue) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if value.runtime != nil {
		return true, nil
	}
	for _, choice := range value.choices {
		if found, err := b.hasRuntimeObjectValue(choice.value); found || err != nil {
			return found, err
		}
	}
	if value.object != nil {
		for _, field := range *value.object {
			if found, err := b.hasRuntimeObjectValue(field.value); found || err != nil {
				return found, err
			}
		}
	}
	if value.list != nil {
		for _, arm := range value.list.arms {
			if found, err := b.hasRuntimeObjectValue(deferredLeaf(arm.value)); found || err != nil {
				return found, err
			}
		}
	}
	if value.selection != nil {
		for _, arm := range value.selection.arms {
			if found, err := b.hasRuntimeObjectValue(deferredLeaf(arm.value)); found || err != nil {
				return found, err
			}
		}
		return b.hasRuntimeObjectValue(value.selection.fallback)
	}
	return false, nil
}

func (b *builder) deferredRuntimeObjectEnumeration(n hff.Node) (*jsonDeferredValue, bool, error) {
	name, enumeration := objectEnumeration(n)
	if !enumeration {
		return nil, false, nil
	}
	receiver, _ := n.Child(0)
	possible, err := b.hasDeferredValueSource(receiver)
	if !possible || err != nil {
		return nil, err != nil, err
	}
	probe := b.validationBuilder()
	value, handled, err := probe.deferredDescriptor(receiver)
	if err != nil || !handled {
		return nil, err != nil, err
	}
	runtime, err := probe.hasRuntimeObjectValue(value)
	if !runtime || err != nil {
		return nil, err != nil, err
	}
	result, err := probe.enumerateRuntimeObject(n, value, name)
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return result, true, err
}

func (b *builder) enumerateRuntimeObject(n hff.Node, value *jsonDeferredValue, name string) (*jsonDeferredValue, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, err
	}
	if value.selection != nil {
		return b.mapDeferredSelection(n, value, func(child *jsonDeferredValue) (*jsonDeferredValue, error) {
			return b.enumerateRuntimeObject(n, child, name)
		})
	}
	if value.choices != nil {
		choices := make([]jsonDeferredChoice, 0, len(value.choices))
		for _, choice := range value.choices {
			child, err := b.enumerateRuntimeObject(n, choice.value, name)
			if err != nil {
				return nil, err
			}
			choices = append(choices, jsonDeferredChoice{choice.when, child})
		}
		return makeDeferredChoices(choices, value.present), nil
	}
	var code, object, payload string
	lossy := deferredValueLoss(value)
	if value.runtime != nil {
		code = b.project(value.runtime.code)
		object = "COALESCE((" + code + "->>0)='object',FALSE)"
		payload = "(" + code + "->1)"
	} else if name == "keys" && value.leaf != nil {
		// Keys do not observe numeric subtypes or tagged scalar payloads.
		carrier, err := b.deferredLeafExpression(n, *value.leaf)
		if err != nil {
			return nil, err
		}
		raw, format, err := b.jsonRepresentation(n, carrier)
		if err != nil {
			return nil, err
		}
		object = "COALESCE(" + string(format) + "_typeof(" + raw + ")='object',FALSE)"
		payload = "(" + raw + ")::json"
	} else {
		encoded, err := b.encodeRuntimeValue(n, value)
		if err != nil {
			return nil, err
		}
		code = b.project(encoded)
		object = "COALESCE((" + code + "->>0)='object',FALSE)"
		payload = "(" + code + "->1)"
	}
	entry, rows := b.nextAlias(), b.nextAlias()
	from := "(SELECT " + entry + ".key," + entry + ".value,row_number() OVER(ORDER BY " + entry + ".key COLLATE \"C\") AS ord FROM json_each(CASE WHEN " + object + " THEN " + payload + " ELSE '{}'::json END) AS " + entry + "(key,value)) AS " + rows
	typ := jsonStoredMemberType(value.typ, "", false, false)
	child := runtimeValue(rows+".value", typ, "TRUE", lossy)
	if name == "keys" {
		child = runtimeValue(runtimeNode("string", rows+".key"), hff.PrimitiveType(hff.StringType), "TRUE", false)
	} else if name == "items" {
		pair := runtimeNode("list", "json_build_array("+runtimeNode("string", rows+".key")+","+rows+".value)")
		child = runtimeValue(pair, hff.NewListType(hff.NewUnionType(hff.PrimitiveType(hff.StringType), typ)), "TRUE", lossy)
		child.runtime.itemRoles = &jsonItemRoles{value: typ, object: value.typ}
	}
	plan := &jsonDeferredList{null: "(NOT " + object + ")", present: value.present, arms: []jsonDeferredArm{{
		from: from, ordinal: []string{rows + ".ord"}, selected: "TRUE", value: deferredValueParameter(child), parameters: copyJSONParameters(b.parameters),
	}}}
	return &jsonDeferredValue{typ: n.Type(), present: value.present, list: plan}, nil
}
