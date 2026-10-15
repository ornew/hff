package sql

import (
	"math"
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// Literal numeric indexing is an observation of a complete child, not of the
// list carrier. Keep captures lexical and reject any additional whole-item use.
func (b *builder) observedDeferredFoldIndexes(n hff.Node, name string) ([]float64, error) {
	if b.disableDeferredFoldPruning {
		return nil, nil
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return nil, nil
	}
	item := 1
	if name == "foldr" {
		item = 0
	}
	id := parameters[item].ID()
	var indexes []float64
	var scan func(hff.Node) (bool, error)
	scan = func(node hff.Node) (bool, error) {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if node.Kind() == hff.IndexNode {
			parent, _ := node.Child(0)
			if binding, ok := parent.Binding(); ok && binding.ID() == id {
				index, _ := node.Child(1)
				x, known := itemLiteralIndex(index)
				if !known || math.IsNaN(x) || math.IsInf(x, 0) || math.Trunc(x) != x {
					return false, nil
				}
				indexes = append(indexes, x)
				return true, nil
			}
		}
		if binding, ok := node.Binding(); ok && binding.ID() == id {
			return false, nil
		}
		for i := 0; i < node.ChildCount(); i++ {
			child, _ := node.Child(i)
			if ok, err := scan(child); err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	}
	body, _ := callback.Child(0)
	if ok, err := scan(body); err != nil || !ok || len(indexes) == 0 {
		return nil, err
	}
	if ok, err := b.builtinDeferredFoldProjection(n); err != nil || !ok {
		return nil, err
	}
	return indexes, nil
}

// Constructor positions can repeat the same arm. This is a lexical child
// proof, not an inference from a homogeneous element schema or an arm count.
func (b *builder) fixedDeferredFoldPositions(value *jsonDeferredValue) ([]int, bool, error) {
	positions, fixed := deferredLiteralPositions(value)
	if !fixed || len(positions) == 0 {
		return nil, false, nil
	}
	used := make([]bool, len(value.list.arms))
	for _, index := range positions {
		if err := checkContext(b.ctx); err != nil {
			return nil, false, err
		}
		if index < 0 || index >= len(used) {
			return nil, false, nil
		}
		arm := value.list.arms[index]
		if arm.empty || arm.selected != "TRUE" || len(arm.steps) != 0 {
			return nil, false, nil
		}
		used[index] = true
	}
	for i, arm := range value.list.arms {
		if err := checkContext(b.ctx); err != nil {
			return nil, false, err
		}
		if !arm.empty && !used[i] {
			return nil, false, nil
		}
	}
	return positions, true, nil
}

func (b *builder) hasFixedDeferredFoldInputs(plan jsonDeferredList) (bool, error) {
	for _, arm := range plan.arms {
		if _, fixed, err := b.fixedDeferredFoldPositions(deferredLeaf(arm.value)); err != nil || fixed {
			return fixed, err
		}
	}
	return false, nil
}

func foldIndexObserves(indexes []float64, position, length int) bool {
	for _, x := range indexes {
		if x < 0 {
			x += float64(length)
		}
		if x == float64(position) {
			return true
		}
	}
	return false
}

// Only selected unique arms cross the fold scope. The decoder restores one
// semantic arm per original position, including repeated and unused children.
func (b *builder) packFixedDeferredFoldList(n hff.Node, value *jsonDeferredValue, positions []int, indexes []float64) (jsonDeferredPacket, error) {
	typ := value.typ
	packets := make(map[int]jsonDeferredPacket)
	payloads := make(map[int]int)
	types := make([]hff.Type, len(positions))
	var codes []string
	for position, armIndex := range positions {
		if err := checkContext(b.ctx); err != nil {
			return jsonDeferredPacket{}, err
		}
		arm := value.list.arms[armIndex]
		types[position] = arm.value.typ
		if !foldIndexObserves(indexes, position, len(positions)) {
			continue
		}
		if _, packed := packets[armIndex]; packed {
			continue
		}
		var packet jsonDeferredPacket
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			packet, err = b.packDeferredValue(n, deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return jsonDeferredPacket{}, err
		}
		// Constructor children belong to the enclosing lexical scope. Any
		// new packet projections therefore belong here, without arm.from.
		b.steps = append(b.steps, steps...)
		payloads[armIndex] = len(codes)
		codes = append(codes, packet.code)
		packet.code = ""
		packets[armIndex] = packet
	}
	code := "'[]'::json"
	if len(codes) > 0 {
		code = "array_to_json(ARRAY[" + strings.Join(codes, ",") + "]::json[])"
	}
	code = "(CASE WHEN " + value.list.null + " THEN 'null'::json ELSE " + code + " END)"
	// Positions are immutable metadata; do not retain source list descriptors
	// or aliases in the decoder closure.
	positions = append([]int(nil), positions...)
	return jsonDeferredPacket{code: code, typ: typ,
		decode: func(b *builder, n hff.Node, code string) (*jsonDeferredValue, error) {
			plan := &jsonDeferredList{null: "FALSE", present: "TRUE", literal: true}
			decoded := make(map[int]*jsonDeferredValue)
			for position, armIndex := range positions {
				if err := checkContext(b.ctx); err != nil {
					return nil, err
				}
				child := decoded[armIndex]
				if child == nil {
					packet, selected := packets[armIndex]
					payload := "'null'::json"
					if selected {
						payload = "(" + code + "->" + strconv.Itoa(payloads[armIndex]) + ")"
					} else {
						packet = unusedDeferredFoldPacket(types[position])
					}
					var err error
					child, err = packet.decode(b, n, payload)
					if err != nil {
						return nil, err
					}
					decoded[armIndex] = child
				}
				alias := b.nextAlias()
				plan.arms = append(plan.arms, jsonDeferredArm{from: "(VALUES (1)) AS " + alias + "(n)", ordinal: []string{strconv.Itoa(position)}, selected: "TRUE", value: deferredValueParameter(child), parameters: copyJSONParameters(b.parameters)})
			}
			list := &jsonDeferredValue{typ: typ, present: "TRUE", list: plan}
			// Literal access does not test plan.null itself. Keep a nullable
			// packet behind choices before exposing its fixed position view.
			out := makeDeferredChoices([]jsonDeferredChoice{{"(json_typeof(" + code + ")='array')", list}, {"(json_typeof(" + code + ")='null')", deferredNull()}}, "TRUE")
			out.typ = typ
			return out, nil
		}}, nil
}
