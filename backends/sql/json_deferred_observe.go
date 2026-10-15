package sql

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) deferredPredicate(n hff.Node) (expression, error) {
	if slot, handled, err := b.jsonItemSlot(n); handled || err != nil {
		if err != nil {
			return expression{}, err
		}
		return b.jsonItemSlotTruth(n, slot)
	}
	if value, handled, err := b.deferredDescriptor(n); handled || err != nil {
		if err != nil {
			return expression{}, err
		}
		return b.observeDeferredValue(n, value, "truth")
	}
	return b.expression(n)
}

func (b *builder) observeDeferredValue(n hff.Node, value *jsonDeferredValue, name string) (expression, error) {
	if err := checkContext(b.ctx); err != nil {
		return expression{}, err
	}
	if value.runtime != nil {
		return b.observeRuntimeValue(n, value, name)
	}
	if value.selection != nil {
		return b.observeDeferredSelection(n, value, name)
	}
	if value.choices != nil {
		codes := make([]string, len(value.choices))
		presence := make([]string, len(value.choices))
		kind, fallback := hff.StringType, "'null'::text"
		if name == "truth" || name == "exists" {
			kind, fallback = hff.BoolType, "FALSE"
		} else if name == "size" || name == "count" || name == "len" {
			kind, fallback = hff.IntType, "NULL::bigint"
		}
		for i, choice := range value.choices {
			result, err := b.observeDeferredValue(n, choice.value, name)
			if err != nil {
				return expression{}, err
			}
			codes[i], presence[i] = result.code, result.present
		}
		present := both(value.present, deferredCases(value.choices, presence, "TRUE"))
		code := deferredCases(value.choices, codes, fallback)
		if name == "truth" || name == "exists" {
			return boolean(both(present, "COALESCE("+code+",FALSE)")), nil
		}
		return expression{code, present, kind}, nil
	}
	if value.leaf != nil {
		return b.observeDeferredLeaf(n, value, name)
	}
	null, size, typeName := "FALSE", "0::bigint", "object"
	if value.list != nil {
		null, typeName = value.list.null, "list"
		if name == "size" || name == "count" || name == "truth" {
			var counts []string
			for _, arm := range value.list.arms {
				if err := checkContext(b.ctx); err != nil {
					return expression{}, err
				}
				counts = append(counts, "(SELECT count(*)::numeric FROM "+deferredFrom(arm)+" WHERE "+arm.selected+")")
			}
			if len(counts) > 0 {
				total := b.project("(" + strings.Join(counts, "+") + ")")
				size = "(CASE WHEN " + total + "<=9223372036854775807::numeric THEN " + total + "::bigint ELSE NULL::bigint END)"
			}
		}
	} else if value.object != nil {
		size = strconv.Itoa(len(*value.object)) + "::bigint"
		if name == "count" {
			size = "NULL::bigint"
		}
	}
	switch name {
	case "type":
		return expression{"(CASE WHEN " + null + " THEN 'null'::text ELSE '" + typeName + "'::text END)", value.present, hff.StringType}, nil
	case "exists":
		return boolean(both(value.present, "(NOT "+null+")")), nil
	case "truth":
		return boolean(both(value.present, "(NOT "+null+" AND COALESCE("+size+">0,FALSE))")), nil
	case "len":
		return expression{"NULL::bigint", value.present, hff.IntType}, nil
	case "size", "count":
		return expression{"(CASE WHEN " + null + " THEN NULL::bigint ELSE " + size + " END)", value.present, hff.IntType}, nil
	case "json", "string":
		code, err := b.serializeDeferredValue(n, value)
		return expression{code, value.present, hff.StringType}, err
	}
	return expression{}, b.failure(n, "unsupported deferred value observer")
}

func (b *builder) observeDeferredLeaf(n hff.Node, value *jsonDeferredValue, name string) (expression, error) {
	parameter := *value.leaf
	// Float decoding can produce ordinary null even for a declared field.
	// Preserve that boundary before observing the root category or presence.
	if parameter.declared && parameter.direct == nil && !jsonRootHasKind(value.typ, hff.FloatType) {
		if name == "type" {
			code, err := b.jsonRuntimeTypeName(n, parameter.code, parameter.format, value.typ)
			return expression{code, value.present, hff.StringType}, err
		}
		if name == "exists" {
			if err := b.checkJSONStringSchemas(n, value.typ); err != nil {
				return expression{}, err
			}
			return boolean(both(value.present, "("+parameter.code+" IS NOT NULL AND "+string(parameter.format)+"_typeof("+parameter.code+")<>'null')")), nil
		}
	}
	if name == "len" {
		return b.deferredLength(n, value)
	}
	if parameter.itemSlot != nil {
		slot := *parameter.itemSlot
		slot.present = value.present
		if name == "truth" {
			return b.jsonItemSlotTruth(n, slot)
		}
		if name == "exists" {
			return boolean(both(value.present, "(NOT "+slot.null()+")")), nil
		}
		nullResult, kind := "'null'::text", hff.StringType
		if name == "size" || name == "count" {
			nullResult, kind = "NULL::bigint", hff.IntType
		}
		result, err := b.jsonItemSlotOperation(n, slot, nullResult, func(source hff.Type) (string, error) {
			if name == "type" {
				return b.jsonRuntimeTypeName(n, slot.raw, slot.format, source)
			}
			carrier, err := b.jsonDecodeProjected(n, slot.raw, slot.format, source, "TRUE")
			if err != nil {
				return "", err
			}
			if name == "size" || name == "count" {
				return b.deferredLeafSize(carrier, slot.format, name), nil
			}
			out, err := b.serializeStoredValue(n, carrier, name, source, jsonHasFloat(source) && (parameter.lossy || parameter.format == JSONB), false)
			return out.code, err
		})
		return expression{result, value.present, kind}, err
	}
	carrier, err := b.deferredLeafExpression(n, parameter)
	if err != nil {
		return expression{}, err
	}
	carrier.present = value.present
	switch name {
	case "truth":
		return boolean(b.truthWithType(carrier, value.typ)), nil
	case "exists":
		return boolean(both(value.present, "("+carrier.code+" IS NOT NULL)")), nil
	case "type":
		raw, format, err := b.jsonRepresentation(n, carrier)
		if err != nil {
			return expression{}, err
		}
		code, err := b.jsonRuntimeTypeName(n, raw, format, value.typ)
		return expression{code, value.present, hff.StringType}, err
	case "json", "string":
		return b.serializeStoredValue(n, carrier, name, value.typ, parameter.lossy || parameter.format == JSONB && jsonHasFloat(value.typ), parameter.itemRoles != nil || parameter.itemListRoles != nil)
	case "size", "count":
		return expression{b.deferredLeafSize(carrier, b.jsonValues[carrier.code], name), value.present, hff.IntType}, nil
	}
	return expression{}, b.failure(n, "deferred scalar does not support this collection observer")
}

func (b *builder) deferredLeafSize(value expression, format JSONFormat, name string) string {
	if format == "" {
		return "NULL::bigint"
	}
	class := string(format) + "_typeof(" + value.code + ")"
	list := "(CASE WHEN " + class + "='array' THEN json_array_length(" + value.code + "::json)::bigint"
	if name == "size" {
		list += " WHEN " + class + "='object' THEN (SELECT count(*) FROM json_object_keys(" + value.code + "::json))"
	}
	return list + " ELSE NULL::bigint END)"
}

func (b *builder) serializeDeferredValue(n hff.Node, value *jsonDeferredValue) (string, error) {
	if value.leaf != nil || value.choices != nil || value.selection != nil || value.runtime != nil {
		result, err := b.observeDeferredValue(n, value, "json")
		return result.code, err
	}
	if value.list != nil {
		plan, err := b.deferredValueList(n, value)
		if err != nil {
			return "", err
		}
		var rows []string
		for _, arm := range plan.arms {
			var code string
			steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
				var err error
				code, err = b.serializeDeferredValue(n, deferredLeaf(arm.value))
				return err
			})
			if err != nil {
				return "", err
			}
			arm.steps = steps
			rows = append(rows, "SELECT "+deferredOrdinal(arm)+" AS ord,"+code+" AS v FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
		}
		if len(rows) == 0 {
			return "(CASE WHEN " + plan.null + " THEN 'null' ELSE '[]' END)", nil
		}
		alias := b.nextAlias()
		return "(CASE WHEN " + plan.null + " THEN 'null' ELSE (SELECT '['||COALESCE(string_agg(v,',' ORDER BY ord),'')||']' FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + alias + ") END)", nil
	}
	fields := append([]jsonDeferredField(nil), (*value.object)...)
	sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })
	parts := []string{"'{'::text"}
	for i, field := range fields {
		key, err := b.parameter(n, hff.String(field.name))
		if err != nil {
			return "", err
		}
		child, err := b.serializeDeferredValue(n, field.value)
		if err != nil {
			return "", err
		}
		if i > 0 {
			parts = append(parts, "','::text")
		}
		parts = append(parts, "to_json("+key.code+"::text)::text", "':'::text", child)
	}
	parts = append(parts, "'}'::text")
	return "(" + strings.Join(parts, "||") + ")", nil
}

// Length observes only the root String or Bytes value. Unrelated container
// children need not be serialized or recovered for an inapplicable call.
func (b *builder) deferredLength(n hff.Node, value *jsonDeferredValue) (expression, error) {
	parameter := *value.leaf
	if parameter.itemSlot != nil {
		slot := *parameter.itemSlot
		code, err := b.jsonItemSlotOperation(n, slot, "NULL::bigint", func(source hff.Type) (string, error) { return b.storedLength(n, slot.raw, slot.format, source) })
		return expression{code, value.present, hff.IntType}, err
	}
	carrier, err := b.deferredLeafExpression(n, parameter)
	if err != nil {
		return expression{}, err
	}
	switch carrier.kind {
	case hff.NullType:
		return expression{"NULL", value.present, hff.NullType}, nil
	case hff.StringType:
		return expression{"char_length(" + carrier.code + ")::bigint", value.present, hff.IntType}, nil
	case hff.BytesType:
		return expression{"octet_length(" + carrier.code + ")::bigint", value.present, hff.IntType}, nil
	case hff.AnyType:
		raw, format, err := b.jsonRepresentation(n, carrier)
		if err != nil {
			return expression{}, err
		}
		code, err := b.storedLength(n, raw, format, value.typ)
		return expression{code, value.present, hff.IntType}, err
	}
	return expression{"NULL::bigint", value.present, hff.IntType}, nil
}

func (b *builder) storedLength(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	textPossible := jsonRootHasKind(typ, hff.AnyType) || jsonRootHasKind(typ, hff.StringType)
	bytesPossible := jsonRootHasKind(typ, hff.BytesType)
	if !textPossible && !bytesPossible {
		return "NULL::bigint", checkContext(b.ctx)
	}
	// Only root string-family ambiguity matters to this scalar observation.
	members := []hff.Type{typ}
	if typ.Kind() == hff.UnionType {
		members = typ.Members()
	}
	var scalars []hff.Type
	for _, member := range members {
		if member.Kind() != hff.ListType && member.Kind() != hff.MapType && member.Kind() != hff.StructType {
			scalars = append(scalars, member)
		}
	}
	if err := b.checkJSONStringSchemas(n, hff.NewUnionType(scalars...)); err != nil {
		return "", err
	}
	text := "(" + raw + " #>> '{}'::text[])"
	length := "char_length(" + text + ")::bigint"
	if bytesPossible {
		length = "octet_length(" + postgresDecodeBase64(text) + ")::bigint"
	}
	return "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='string' THEN " + length + " ELSE NULL::bigint END)", nil
}
