package sql

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// Each observed child owns a slot, not merely a storage kind. Slots may share
// physical columns across input arms, whose decoders retain their own shapes.
// Dense slots count selected children only; original positions live in the
// decoder metadata, so absent fields do not widen the shared SQL layout.
type jsonDeferredFoldNativeChild struct {
	column    jsonDeferredFoldColumn
	parameter jsonParameter
}

type jsonDeferredFoldProjectedChild struct {
	name   string
	typ    hff.Type
	native int // -1 means an unobserved typed placeholder
}

type jsonDeferredFoldProjection struct {
	typ      hff.Type
	null     string
	list     bool
	children []jsonDeferredFoldProjectedChild
	values   []jsonDeferredFoldNativeChild
}

func nativeDeferredFoldChildColumn(slot int, kind hff.TypeKind) jsonDeferredFoldColumn {
	column, _ := nativeDeferredFoldColumn(kind)
	column.name = "child_" + strconv.Itoa(slot) + "_" + column.name
	return column
}

// The observation proof already excludes bare/root uses, dynamic indices and
// custom calls. Prove every emitted root before choosing one shared layout;
// a nonnative observed subtree keeps the original packet path for the fold.
func (b *builder) nativeDeferredFoldProjections(plan jsonDeferredList, fields map[string]bool, indexes []float64) ([]*jsonDeferredFoldProjection, error) {
	if b.disableDeferredFoldPruning || fields == nil && indexes == nil {
		return nil, nil
	}
	projections := make([]*jsonDeferredFoldProjection, len(plan.arms))
	found := false
	for i, arm := range plan.arms {
		if err := checkContext(b.ctx); err != nil {
			return nil, err
		}
		if arm.empty {
			continue
		}
		value := deferredLeaf(arm.value)
		projection := &jsonDeferredFoldProjection{typ: value.typ, null: "FALSE"}
		if fields != nil && value.object != nil {
			for _, field := range *value.object {
				if err := checkContext(b.ctx); err != nil {
					return nil, err
				}
				child := jsonDeferredFoldProjectedChild{name: field.name, typ: field.value.typ, native: -1}
				if fields[field.name] {
					parameter, ok := b.nativeScalarDeferredFoldParameter(deferredValueParameter(field.value))
					if !ok {
						return nil, nil
					}
					child.native = len(projection.values)
					projection.values = append(projection.values, jsonDeferredFoldNativeChild{nativeDeferredFoldChildColumn(child.native, parameter.direct.kind), parameter})
				}
				projection.children = append(projection.children, child)
			}
		} else if indexes != nil {
			positions, fixed, err := b.fixedDeferredFoldPositions(value)
			if err != nil || !fixed {
				return nil, err
			}
			// Match the packet's CASE WHEN null THEN null ELSE list: an SQL
			// unknown condition selects the non-null list, not a third state.
			projection.list, projection.null = true, "COALESCE(("+value.list.null+"),FALSE)"
			selected := make(map[int]int)
			for position, armIndex := range positions {
				if err := checkContext(b.ctx); err != nil {
					return nil, err
				}
				if !foldIndexObserves(indexes, position, len(positions)) {
					continue
				}
				if _, exists := selected[armIndex]; exists {
					continue
				}
				parameter, ok := b.nativeScalarDeferredFoldParameter(value.list.arms[armIndex].value)
				if !ok {
					return nil, nil
				}
				selected[armIndex] = len(projection.values)
				projection.values = append(projection.values, jsonDeferredFoldNativeChild{nativeDeferredFoldChildColumn(selected[armIndex], parameter.direct.kind), parameter})
			}
			for _, armIndex := range positions {
				if err := checkContext(b.ctx); err != nil {
					return nil, err
				}
				child := jsonDeferredFoldProjectedChild{typ: value.list.arms[armIndex].value.typ, native: -1}
				if selected, ok := selected[armIndex]; ok {
					child.native = selected
				}
				projection.children = append(projection.children, child)
			}
		} else {
			return nil, nil
		}
		found = found || len(projection.values) > 0
		projections[i] = projection
	}
	if !found {
		return nil, nil
	}
	return projections, nil
}

func (b *builder) projectedDeferredFoldColumns(projections []*jsonDeferredFoldProjection) ([]jsonDeferredFoldColumn, error) {
	unique := make(map[string]jsonDeferredFoldColumn)
	for _, projection := range projections {
		if projection == nil {
			continue
		}
		for _, value := range projection.values {
			if err := checkContext(b.ctx); err != nil {
				return nil, err
			}
			unique[value.column.name] = value.column
		}
	}
	columns := make([]jsonDeferredFoldColumn, 0, len(unique))
	for _, column := range unique {
		columns = append(columns, column)
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].name < columns[j].name })
	return columns, nil
}

func (p *jsonDeferredFoldProjection) columns(layout []jsonDeferredFoldColumn) string {
	values := make(map[string]string, len(p.values))
	for _, value := range p.values {
		values[value.column.name] = "(" + value.parameter.direct.code + ")"
	}
	columns := []string{"(" + p.null + ")::boolean AS input_null"}
	for _, column := range layout {
		code, ok := values[column.name]
		if !ok {
			code = "NULL"
		}
		columns = append(columns, code+"::"+column.sql+" AS "+column.name)
	}
	return strings.Join(columns, ",")
}

func (t jsonDeferredFoldTransport) gateColumns(scope string) string {
	if t.projection != nil {
		columns := []string{scope + ".input_null"}
		for _, child := range t.projection.values {
			columns = append(columns, scope+"."+child.column.name)
		}
		return strings.Join(columns, ",")
	}
	if t.native != nil {
		return scope + "." + t.nativeColumn() + " AS value"
	}
	return scope + ".value"
}

func (t jsonDeferredFoldTransport) decodeInput(b *builder, n hff.Node, scope string) (jsonParameter, error) {
	if t.projection == nil {
		return t.decode(b, n, scope+".value")
	}
	p := t.projection
	values := make([]*jsonDeferredValue, len(p.values))
	for i, child := range p.values {
		if err := checkContext(b.ctx); err != nil {
			return jsonParameter{}, err
		}
		value := expression{code: scope + "." + child.column.name, present: "TRUE", kind: child.parameter.direct.kind}
		values[i] = deferredLeaf(jsonParameter{typ: child.parameter.typ, lossy: child.parameter.lossy, direct: &value})
	}
	fields := make([]jsonDeferredField, 0, len(p.children))
	plan := &jsonDeferredList{null: "FALSE", present: "TRUE", literal: true}
	for position, metadata := range p.children {
		if err := checkContext(b.ctx); err != nil {
			return jsonParameter{}, err
		}
		var child *jsonDeferredValue
		if metadata.native >= 0 {
			child = values[metadata.native]
		} else {
			var err error
			child, err = unusedDeferredFoldPacket(metadata.typ).decode(b, n, "'null'::json")
			if err != nil {
				return jsonParameter{}, err
			}
		}
		if p.list {
			alias := b.nextAlias()
			plan.arms = append(plan.arms, jsonDeferredArm{from: "(VALUES (1)) AS " + alias + "(n)", ordinal: []string{strconv.Itoa(position)}, selected: "TRUE", value: deferredValueParameter(child), parameters: copyJSONParameters(b.parameters)})
		} else {
			fields = append(fields, jsonDeferredField{metadata.name, child})
		}
	}
	if !p.list {
		return deferredValueParameter(&jsonDeferredValue{typ: p.typ, present: "TRUE", object: &fields}), nil
	}
	list := &jsonDeferredValue{typ: p.typ, present: "TRUE", list: plan}
	null := scope + ".input_null"
	value := makeDeferredChoices([]jsonDeferredChoice{{"(NOT " + null + ")", list}, {null, deferredNull()}}, "TRUE")
	value.typ = p.typ
	return deferredValueParameter(value), nil
}
