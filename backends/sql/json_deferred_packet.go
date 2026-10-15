package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// A packet moves a finite input descriptor between SQL scopes without
// interpreting its payload as untyped JSON. The decoder captures immutable
// schema/role data only; every runtime value comes from the new packet scope.
// This is a private transport, never the representation of a stored Binding.
type jsonDeferredPacket struct {
	code   string
	typ    hff.Type
	decode func(*builder, hff.Node, string) (*jsonDeferredValue, error)
}

func (b *builder) packDeferredValue(n hff.Node, value *jsonDeferredValue) (jsonDeferredPacket, error) {
	typ := value.typ
	if err := checkContext(b.ctx); err != nil {
		return jsonDeferredPacket{}, err
	}
	if value.runtime != nil {
		// Runtime nodes already use a closed recursive carrier. Move the node
		// intact; the decoder must reference only its new lexical packet scope.
		lossy := value.runtime.lossy
		pair, list := value.runtime.itemRoles, value.runtime.itemListRoles
		return jsonDeferredPacket{code: value.runtime.code, typ: typ,
			decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
				value := runtimeValue(code, typ, "TRUE", lossy)
				value.runtime.itemRoles, value.runtime.itemListRoles = pair, list
				return value, checkContext(b.ctx)
			}}, nil
	}
	if value.selection != nil {
		var variants []jsonDeferredPacket
		result, err := b.resolveDeferredSelection(n, value, hff.AnyType, func(child *jsonDeferredValue) (expression, error) {
			packet, err := b.packDeferredValue(n, child)
			if err != nil {
				return expression{}, err
			}
			index := len(variants)
			code := packetVariant(index, packet.code)
			packet.code = ""
			variants = append(variants, packet)
			return expression{code, child.present, hff.AnyType}, nil
		})
		if err != nil {
			return jsonDeferredPacket{}, err
		}
		return deferredVariantPacket(result.code, typ, variants), nil
	}
	if value.choices != nil {
		var variants []jsonDeferredPacket
		var codes []string
		for i, choice := range value.choices {
			packet, err := b.packDeferredValue(n, choice.value)
			if err != nil {
				return jsonDeferredPacket{}, err
			}
			codes = append(codes, packetVariant(i, packet.code))
			packet.code = ""
			variants = append(variants, packet)
		}
		code := deferredCases(value.choices, codes, packetVariant(-1, "'null'::json"))
		return deferredVariantPacket(code, typ, variants), nil
	}
	if value.list != nil {
		return b.packDeferredList(n, value)
	}
	if value.object != nil {
		return b.packDeferredObject(n, value, nil)
	}
	parameter := *value.leaf
	if parameter.itemSlot != nil {
		slot := *parameter.itemSlot
		format, roles := slot.format, slot.roles
		constraints := append([]hff.Type(nil), slot.constraints...)
		metadata := jsonParameter{typ: parameter.typ, lossy: parameter.lossy}
		code := "json_build_array(" + slot.raw + "::json," + slot.pair + "::json," + slot.key + "," + slot.value + ")"
		return jsonDeferredPacket{code: code, typ: typ,
			decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
				slot := &jsonItemSlot{raw: "(" + code + "->0)::" + string(format), pair: "(" + code + "->1)::" + string(format),
					key: "(" + code + "->>2)::boolean", value: "(" + code + "->>3)::boolean", present: "TRUE", format: format, roles: roles,
					constraints: append([]hff.Type(nil), constraints...)}
				return deferredLeaf(jsonParameter{typ: metadata.typ, format: format, lossy: metadata.lossy, itemSlot: slot}), checkContext(b.ctx)
			}}, nil
	}
	var code string
	if parameter.direct != nil {
		var err error
		code, err = b.computedJSONDocument(n, *parameter.direct, parameter.typ)
		if err != nil {
			return jsonDeferredPacket{}, err
		}
	} else {
		code = "COALESCE(" + parameter.code + "::json,'null'::json)"
	}
	// All fields that could reference the source scope are replaced when the
	// decoder runs. Kind/format/loss and positional-role proofs remain static.
	direct := parameter.direct != nil
	metadata := jsonParameter{typ: parameter.typ, format: parameter.format, lossy: parameter.lossy, itemRoles: parameter.itemRoles, itemListRoles: parameter.itemListRoles}
	return jsonDeferredPacket{code: code, typ: typ,
		decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
			p := jsonParameter{typ: metadata.typ, format: metadata.format, lossy: metadata.lossy,
				itemRoles: metadata.itemRoles, itemListRoles: metadata.itemListRoles}
			if direct && metadata.typ.Kind() == hff.NeverType {
				x := expression{"NULL", "TRUE", hff.NullType}
				p.direct = &x
			} else if direct {
				x, err := b.jsonDecode(n, code, JSON, metadata.typ, "TRUE")
				if err != nil {
					return nil, err
				}
				p.direct = &x
			} else {
				p.code = "(" + code + ")::" + string(metadata.format)
			}
			return deferredLeaf(p), checkContext(b.ctx)
		}}, nil
}

func packetVariant(index int, payload string) string {
	return "json_build_array(" + strconv.Itoa(index) + "," + payload + ")"
}

func deferredVariantPacket(code string, typ hff.Type, variants []jsonDeferredPacket) jsonDeferredPacket {
	return jsonDeferredPacket{code: code, typ: typ,
		decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
			choices := make([]jsonDeferredChoice, len(variants))
			for i, packet := range variants {
				payload := "(CASE WHEN (" + code + "->>0)::integer=" + strconv.Itoa(i) + " THEN " + code + "->1 ELSE 'null'::json END)"
				child, err := packet.decode(b, n, payload)
				if err != nil {
					return nil, err
				}
				choices[i] = jsonDeferredChoice{"((" + code + "->>0)::integer=" + strconv.Itoa(i) + ")", child}
			}
			return makeDeferredChoices(choices, "TRUE"), nil
		}}
}

func (b *builder) packDeferredList(n hff.Node, value *jsonDeferredValue) (jsonDeferredPacket, error) {
	typ := value.typ
	plan, err := b.deferredValueList(n, value)
	if err != nil {
		return jsonDeferredPacket{}, err
	}
	var rows []string
	var packets []jsonDeferredPacket
	var empty []bool
	for i, arm := range plan.arms {
		var packet jsonDeferredPacket
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			packet, err = b.packDeferredValue(n, deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return jsonDeferredPacket{}, err
		}
		arm.steps = steps
		if !arm.empty {
			rows = append(rows, "SELECT "+deferredOrdinal(arm)+" AS ord,"+packetVariant(i, packet.code)+" AS v FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
		}
		packet.code = ""
		packets, empty = append(packets, packet), append(empty, arm.empty)
	}
	code := "'[]'::json"
	if len(rows) > 0 {
		alias := b.nextAlias()
		code = "(SELECT COALESCE(json_agg(v ORDER BY ord),'[]'::json) FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + alias + ")"
	}
	code = "(CASE WHEN " + plan.null + " THEN 'null'::json ELSE " + code + " END)"
	return jsonDeferredPacket{code: code, typ: typ,
		decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
			plan := &jsonDeferredList{null: "(json_typeof(" + code + ")='null')", present: "TRUE"}
			for i, packet := range packets {
				alias := b.nextAlias()
				arm := jsonDeferredArm{from: "json_array_elements(CASE WHEN json_typeof(" + code + ")='array' THEN " + code + " ELSE '[]'::json END) WITH ORDINALITY AS " + alias + "(value,ord)",
					ordinal: []string{alias + ".ord"}, selected: "((" + alias + ".value->>0)::integer=" + strconv.Itoa(i) + ")", parameters: copyJSONParameters(b.parameters), empty: empty[i]}
				var child *jsonDeferredValue
				steps, parameters, err := b.inDeferredArm(arm, hff.Node{}, func() error {
					var err error
					payload := "(CASE WHEN (" + alias + ".value->>0)::integer=" + strconv.Itoa(i) + " THEN " + alias + ".value->1 ELSE 'null'::json END)"
					child, err = packet.decode(b, n, payload)
					return err
				})
				if err != nil {
					return nil, err
				}
				arm.steps, arm.parameters, arm.value = steps, parameters, deferredValueParameter(child)
				plan.arms = append(plan.arms, arm)
			}
			return &jsonDeferredValue{typ: typ, present: "TRUE", list: plan}, nil
		}}, nil
}

// Project only this constructed object's transport payload. Selected child
// descriptors retain their complete subtrees; names, positions and the
// original type are preserved even for unobserved fields.
func (b *builder) packDeferredObject(n hff.Node, value *jsonDeferredValue, observed map[string]bool) (jsonDeferredPacket, error) {
	typ := value.typ
	fields := append([]jsonDeferredField(nil), (*value.object)...)
	names := make([]string, len(fields))
	packets := make([]jsonDeferredPacket, len(fields))
	codes := make([]string, len(fields))
	for i, field := range fields {
		var packet jsonDeferredPacket
		var err error
		if observed != nil && !observed[field.name] {
			if err := checkContext(b.ctx); err != nil {
				return jsonDeferredPacket{}, err
			}
			packet = unusedDeferredFoldPacket(field.value.typ)
		} else {
			packet, err = b.packDeferredValue(n, field.value)
		}
		if err != nil {
			return jsonDeferredPacket{}, err
		}
		names[i], codes[i] = field.name, packet.code
		packet.code = ""
		packets[i] = packet
	}
	return jsonDeferredPacket{code: "array_to_json(ARRAY[" + strings.Join(codes, ",") + "]::json[])", typ: typ,
		decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
			output := make([]jsonDeferredField, len(names))
			for i, name := range names {
				child, err := packets[i].decode(b, n, "("+code+"->"+strconv.Itoa(i)+")")
				if err != nil {
					return nil, err
				}
				output[i] = jsonDeferredField{name, child}
			}
			return &jsonDeferredValue{typ: typ, present: "TRUE", object: &output}, nil
		}}, nil
}
