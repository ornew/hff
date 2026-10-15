package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Keep a mapped slot in its original row scope until its consumer recovers a
// value. Serializing every intermediate map would discard the key that tells
// a String from Bytes, Duration, or Timestamp. This pipeline carries lexical
// descriptors instead; it never changes the public JSON storage contract.
func (b *builder) jsonMapPipeline(n hff.Node, name string) (expression, bool, error) {
	source, maps := mapPipelineSource(n)
	if len(maps) == 0 {
		return expression{}, false, nil
	}
	callbacks := make([]hff.Node, 0, len(maps)+1)
	for _, mapping := range maps {
		callback, _ := mapping.Child(1)
		callbacks = append(callbacks, callback)
	}
	callback, _ := n.Child(1)
	callbacks = append(callbacks, callback)
	if !b.mapPipelineHasItemSlot(source, callbacks[:len(callbacks)-1]) {
		return expression{}, false, nil
	}
	value, err := b.expression(source)
	if err != nil || value.kind == hff.NullType {
		return value, true, err
	}
	value, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return expression{}, true, err
	}

	outer, outerPaths, outerParameters, outerMemo := b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo
	b.steps, b.sharedPaths, b.itemRoleMemo = nil, nil, nil
	b.parameters = make(map[hff.BindingID]jsonParameter, len(outerParameters)+len(callbacks))
	for id, parameter := range outerParameters {
		b.parameters[id] = parameter
	}
	defer func() {
		b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo = outer, outerPaths, outerParameters, outerMemo
	}()

	alias := b.nextAlias()
	parameter := jsonParameter{code: alias + ".value", format: format,
		typ:   jsonStoredMemberType(sortListType(b.jsonStoredType(source)), "", false, true),
		lossy: b.losesFloat(source, nil), itemRoles: b.jsonItemsRoles(source)}
	valid := "TRUE"
	var result expression
	var body hff.Node
	for i, callback := range callbacks {
		parameters, ok := callback.Parameters()
		if !ok || len(parameters) != 1 {
			return expression{}, true, b.failure(n, "map pipeline requires one callback parameter per stage")
		}
		id := parameters[0].ID()
		b.parameters[id] = parameter
		// Earlier stages remain in scope for captured descriptors, but their
		// role memo cannot be reused under a new callback environment.
		b.itemRoleMemo = nil
		body, _ = callback.Child(0)
		if i < len(callbacks)-1 || name != "map" {
			slot, handled, err := b.jsonItemSlot(body)
			if err != nil {
				return expression{}, true, err
			}
			if handled {
				if i == len(callbacks)-1 {
					result, err = b.jsonItemSlotTruth(body, slot)
					if err != nil {
						return expression{}, true, err
					}
					break
				}
				valid = both(valid, slot.present)
				parameter = jsonParameter{typ: b.jsonStoredType(body), format: slot.format,
					lossy: b.losesFloat(body, nil), itemSlot: &slot}
				continue
			}
		}
		result, err = b.expression(body)
		if err != nil {
			return expression{}, true, err
		}
		if i < len(callbacks)-1 || name == "map" {
			// A prior map must finish successfully even when the next body
			// ignores its parameter or its predicate would match earlier.
			valid = both(valid, result.present)
		}
		if i < len(callbacks)-1 {
			// Match a materialized map's normalization boundary before the
			// next callback. A container or dynamic carrier may still hold
			// raw numeric tokens even when its outer SQL kind is known.
			stored := b.jsonStoredType(body)
			if _, structured := b.jsonValues[result.code]; structured {
				raw, err := b.computedJSONDocument(body, result, stored)
				if err != nil {
					return expression{}, true, err
				}
				raw = b.project(raw)
				b.rememberNormalizedJSON(raw, JSON, stored)
				result, err = b.jsonDecodeProjected(body, raw, JSON, stored, result.present)
				if err != nil {
					return expression{}, true, err
				}
			}
			copy := result
			parameter = jsonParameter{typ: stored, lossy: b.losesFloat(body, nil),
				format: b.jsonValues[result.code], direct: &copy,
				itemRoles: b.jsonItemRoles(body), itemListRoles: b.jsonItemsRoles(body)}
		}
	}

	stored := b.jsonStoredType(body)
	var raw string
	if name == "map" {
		raw, err = b.computedJSONDocument(body, result, stored)
		if err != nil {
			return expression{}, true, err
		}
	} else {
		raw = b.truth(body, result)
	}
	from := string(format) + "_array_elements(" + value.code + ") WITH ORDINALITY AS " + alias + "(value,ord)"
	if len(b.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	}
	rows, output := b.nextAlias(), b.nextAlias()
	cte := "WITH " + rows + " AS MATERIALIZED (SELECT " + alias + ".ord AS ord," + raw + " AS v," + valid + " AS p FROM " + from + ")"
	present := both(value.present, "("+value.code+" IS NULL OR (SELECT COALESCE(bool_and(p),TRUE) FROM "+rows+"))")
	var aggregate string
	kind := hff.BoolType
	switch name {
	case "map":
		kind = hff.ListType
		order := " ORDER BY ord"
		if b.orderBlind[n] {
			order = ""
		}
		aggregate = "(SELECT COALESCE(json_agg(v" + order + "),'[]'::json) FROM " + rows + ")"
	case "any":
		aggregate = "(SELECT COALESCE(bool_or(v),FALSE) FROM " + rows + ")"
	case "all":
		aggregate = "(SELECT COALESCE(bool_and(v),TRUE) FROM " + rows + ")"
	case "count":
		kind = hff.IntType
		aggregate = "(SELECT count(*) FROM " + rows + " WHERE v)"
	default:
		return expression{}, true, b.failure(n, "unsupported map pipeline consumer")
	}
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL ELSE " + aggregate + " END)"
	outer = append(outer, "("+cte+" SELECT "+code+" AS v,"+present+" AS p OFFSET 0) AS "+output)
	if name == "map" {
		b.jsonValues[output+".v"] = JSON
		b.rememberNormalizedJSON(output+".v", JSON, hff.NewListType(stored))
	}
	return expression{code: output + ".v", present: output + ".p", kind: kind}, true, nil
}

// Only direct built-in map stages share this row pipeline. Other operations
// retain their own receiver validation and collection materialization rules.
func mapPipelineSource(n hff.Node) (hff.Node, []hff.Node) {
	receiver, _ := n.Child(0)
	var maps []hff.Node
	for {
		method, ok := receiver.Method()
		if !ok || method.Name() != "map" {
			break
		}
		for _, signature := range method.Signatures() {
			if _, custom := signature.Declaration(); custom {
				return receiver, nil
			}
		}
		maps = append(maps, receiver)
		receiver, _ = receiver.Child(0)
	}
	for i, j := 0, len(maps)-1; i < j; i, j = i+1, j-1 {
		maps[i], maps[j] = maps[j], maps[i]
	}
	return receiver, maps
}

// Ordinary chains keep the existing lowering, including its key-only row
// optimization. Activate this path only when a value slot needs its origin.
func (b *builder) mapPipelineHasItemSlot(source hff.Node, callbacks []hff.Node) bool {
	typ := jsonStoredMemberType(sortListType(b.jsonStoredType(source)), "", false, true)
	pair, list := b.jsonItemsRoles(source), (*jsonItemRoles)(nil)
	var restores []func()
	defer func() {
		for i := len(restores) - 1; i >= 0; i-- {
			restores[i]()
		}
	}()
	for _, callback := range callbacks {
		restores = append(restores, b.jsonCallbackParameters(callback, []hff.Type{typ}, []*jsonItemRoles{pair}, []*jsonItemRoles{list}))
		body, _ := callback.Child(0)
		origin, _ := itemSlotOrigin(body)
		if origin.Kind() == hff.IndexNode {
			parent, _ := origin.Child(0)
			index, _ := origin.Child(1)
			x, literal := itemLiteralIndex(index)
			if (!literal || x == 1 || x == -1) && b.jsonItemRoles(parent) != nil {
				return true
			}
		}
		typ, pair, list = b.jsonStoredType(body), b.jsonItemRoles(body), b.jsonItemsRoles(body)
	}
	return false
}
