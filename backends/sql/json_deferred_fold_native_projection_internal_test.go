package sql

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func nativeProjectionObject(fields ...jsonDeferredField) *jsonDeferredValue {
	types := make([]hff.Field, len(fields))
	for i, field := range fields {
		types[i] = hff.Field{Name: field.name, Type: field.value.typ}
	}
	return &jsonDeferredValue{typ: hff.NewStructType(types...), present: "source.parent", object: &fields}
}

func TestNativeChildProjectionKeepsPerArmSlotsAndMetadata(t *testing.T) {
	var fields []jsonDeferredField
	observed := map[string]bool{"missing": true}
	for i, kind := range []hff.TypeKind{hff.IntType, hff.FloatType, hff.BoolType, hff.StringType, hff.BytesType, hff.DurationType, hff.TimestampType, hff.IntType} {
		name := "field_" + strconv.Itoa(i)
		observed[name] = true
		value := expression{code: "source.value_" + strconv.Itoa(i), present: "source.child", kind: kind}
		fields = append(fields, jsonDeferredField{name, deferredLeaf(jsonParameter{typ: schema.NullableType(hff.PrimitiveType(kind)), direct: &value, lossy: true})})
	}
	fields = append(fields, jsonDeferredField{"unobserved", deferredLeaf(jsonParameter{typ: schema.AnyType, code: "NEVER_TRANSPORT", format: JSON})})
	first := nativeProjectionObject(fields...)
	reversed := append([]jsonDeferredField(nil), fields...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	second := nativeProjectionObject(reversed...)
	b := builder{ctx: context.Background(), parameters: map[hff.BindingID]jsonParameter{777: {typ: schema.StringType, code: "outer.value", format: JSON}}, steps: []string{"outer_step"}, args: []any{"outer"}}
	snapshot := copyJSONParameters(b.parameters)
	projections, err := b.nativeDeferredFoldProjections(jsonDeferredList{arms: []jsonDeferredArm{{value: deferredValueParameter(first)}, {value: deferredValueParameter(second)}, {empty: true, value: jsonParameter{typ: schema.AnyType}}}}, observed, nil)
	if err != nil || len(projections) != 3 || projections[2] != nil {
		t.Fatal("matching nullable native children rejected", projections, err)
	}
	layout, err := b.projectedDeferredFoldColumns(projections)
	if err != nil {
		t.Fatal(err)
	}
	for arm, original := range []*jsonDeferredValue{first, second} {
		projection := projections[arm]
		if len(projection.values) != 8 || len(projection.children) != 9 {
			t.Fatal("same-kind fields merged or unobserved child lost", projection)
		}
		row := projection.columns(layout)
		if strings.Contains(row, "NEVER_TRANSPORT") || strings.Count(row, " AS ") != len(layout)+1 || !strings.Contains(row, "::numeric AS ") || strings.Contains(row, "json") {
			t.Fatal("row has wrong shape or carrier", row)
		}
		for _, child := range projection.values {
			if strings.Count(row, child.parameter.direct.code) != 1 {
				t.Fatal("selected child duplicated or omitted", row, child)
			}
		}
		decoded, err := (jsonDeferredFoldTransport{projection: projection}).decodeInput(&b, hff.Node{}, "callback")
		if err != nil || decoded.deferred == nil || !decoded.typ.Equal(original.typ) || decoded.deferred.object == nil || len(*decoded.deferred.object) != 9 {
			t.Fatal("object metadata changed", decoded, err)
		}
		for i, field := range *decoded.deferred.object {
			old := (*original.object)[i]
			if field.name != old.name || !field.value.typ.Equal(old.value.typ) || field.value.present != "TRUE" {
				t.Fatal("original field name, order or type changed", field, old)
			}
			if field.name == "unobserved" {
				continue
			}
			p := field.value.leaf
			if p.direct == nil || p.direct.kind != old.value.leaf.direct.kind || !p.lossy || !strings.HasPrefix(p.direct.code, "callback.child_") || p.direct.present != "TRUE" || p.code != "" || p.format != "" || p.itemSlot != nil {
				t.Fatal("child metadata or lexical scope changed", p)
			}
		}
	}
	if !reflect.DeepEqual(b.parameters, snapshot) || !reflect.DeepEqual(b.steps, []string{"outer_step"}) || !reflect.DeepEqual(b.args, []any{"outer"}) || fields[0].value.leaf.direct.code != "source.value_0" || fields[0].value.leaf.direct.present != "source.child" || first.present != "source.parent" {
		t.Fatal("source descriptor or outer environment mutated")
	}
}

func TestNativeChildProjectionKeepsFixedPositionsAndParentNull(t *testing.T) {
	unused := jsonParameter{typ: schema.AnyType, code: "UNOBSERVED", format: JSON}
	duration := jsonParameter{typ: schema.NullableType(schema.DurationType), direct: &expression{"source.duration", "source.child", hff.DurationType}}
	text := jsonParameter{typ: schema.NullableType(schema.StringType), direct: &expression{"source.text", "source.child", hff.StringType}, lossy: true}
	typ := schema.ListOf(schema.Union(schema.AnyType, schema.DurationType, schema.StringType))
	positions := []int{2, 0, 2, 1}
	list := &jsonDeferredList{null: "source.null", present: "source.present", literalPositions: positions, arms: []jsonDeferredArm{{selected: "TRUE", value: unused}, {selected: "TRUE", value: duration}, {selected: "TRUE", value: text}}}
	original := &jsonDeferredValue{typ: typ, present: "source.parent", list: list}
	b := builder{ctx: context.Background()}
	projections, err := b.nativeDeferredFoldProjections(jsonDeferredList{arms: []jsonDeferredArm{{value: deferredValueParameter(original)}}}, nil, []float64{0, 2, -1, 100})
	if err != nil || len(projections) != 1 || len(projections[0].values) != 2 {
		t.Fatal("repeated children packed twice or shape rejected", projections, err)
	}
	layout, _ := b.projectedDeferredFoldColumns(projections)
	row := projections[0].columns(layout)
	if strings.Count(row, "source.text") != 1 || strings.Count(row, "source.duration") != 1 || strings.Contains(row, "UNOBSERVED") || !strings.Contains(row, "COALESCE((source.null),FALSE)") {
		t.Fatal("fixed child projection changed null semantics or multiplicity", row)
	}
	decoded, err := (jsonDeferredFoldTransport{projection: projections[0]}).decodeInput(&b, hff.Node{}, "callback")
	if err != nil || decoded.deferred == nil || len(decoded.deferred.choices) != 2 || !decoded.typ.Equal(typ) {
		t.Fatal("nullable list exposed directly", decoded, err)
	}
	choices := decoded.deferred.choices
	if choices[0].when != "(NOT callback.input_null)" || choices[1].when != "callback.input_null" || choices[1].value.leaf.direct.kind != hff.NullType {
		t.Fatal("parent null or lexical scope changed", choices)
	}
	plan := choices[0].value.list
	if !plan.literal || plan.null != "FALSE" || len(plan.arms) != len(positions) || !reflect.DeepEqual(plan.arms[0].value, plan.arms[2].value) || !plan.arms[1].value.typ.Equal(unused.typ) || plan.arms[3].value.direct.kind != hff.DurationType || !plan.arms[0].value.lossy {
		t.Fatal("semantic positions, repeated identity or metadata changed", plan)
	}
	if !reflect.DeepEqual(positions, []int{2, 0, 2, 1}) || list.literal || original.present != "source.parent" || text.direct.code != "source.text" {
		t.Fatal("source list mutated")
	}
}

func TestNativeChildProjectionRequiresEveryObservedChild(t *testing.T) {
	b := builder{ctx: context.Background()}
	native := deferredLeaf(jsonParameter{typ: schema.IntType, direct: &expression{"source.value", "TRUE", hff.IntType}})
	nonnative := deferredLeaf(jsonParameter{typ: schema.IntType, code: "source.value", format: JSON})
	for _, plan := range []jsonDeferredList{
		{arms: []jsonDeferredArm{{value: deferredValueParameter(nativeProjectionObject(jsonDeferredField{"a", nonnative}))}}},
		{arms: []jsonDeferredArm{{value: deferredValueParameter(nativeProjectionObject(jsonDeferredField{"a", native}))}, {value: deferredValueParameter(nativeProjectionObject(jsonDeferredField{"a", nonnative}))}}},
		{arms: []jsonDeferredArm{{value: deferredValueParameter(nativeProjectionObject(jsonDeferredField{"other", native}))}}},
		{arms: []jsonDeferredArm{{value: deferredValueParameter(native)}}},
	} {
		if projections, err := b.nativeDeferredFoldProjections(plan, map[string]bool{"a": true}, nil); projections != nil || err != nil {
			t.Fatal("unproven/missing-only projection admitted", projections, err)
		}
	}
	plan := jsonDeferredList{arms: []jsonDeferredArm{{value: deferredValueParameter(nativeProjectionObject(jsonDeferredField{"a", native}))}}}
	b.disableDeferredFoldPruning = true
	if projections, err := b.nativeDeferredFoldProjections(plan, map[string]bool{"a": true}, nil); projections != nil || err != nil {
		t.Fatal("validation probe re-entered native child projection", projections, err)
	}
}

func TestNativeChildProjectionSparseShapesUseDenseSlots(t *testing.T) {
	b := builder{ctx: context.Background()}
	for _, list := range []bool{false, true} {
		var arms []jsonDeferredArm
		for length := 1; length <= 20; length++ {
			var fields []jsonDeferredField
			var children []jsonDeferredArm
			for i := 0; i < length; i++ {
				name := "unused_" + strconv.Itoa(i)
				parameter := jsonParameter{typ: schema.AnyType, code: "UNOBSERVED", format: JSON}
				if i == length-1 {
					name = "used"
					parameter = jsonParameter{typ: schema.IntType, direct: &expression{"source.value", "TRUE", hff.IntType}}
				}
				fields = append(fields, jsonDeferredField{name, deferredLeaf(parameter)})
				children = append(children, jsonDeferredArm{selected: "TRUE", value: parameter})
			}
			value := nativeProjectionObject(fields...)
			if list {
				value = &jsonDeferredValue{typ: schema.ListOf(schema.AnyType), present: "TRUE", list: &jsonDeferredList{null: "FALSE", present: "TRUE", literal: true, arms: children}}
			}
			arms = append(arms, jsonDeferredArm{value: deferredValueParameter(value)})
		}
		fields, indexes := map[string]bool{"used": true}, []float64(nil)
		if list {
			fields, indexes = nil, []float64{-1}
		}
		projections, err := b.nativeDeferredFoldProjections(jsonDeferredList{arms: arms}, fields, indexes)
		if err != nil || len(projections) != len(arms) {
			t.Fatal(list, err)
		}
		layout, err := b.projectedDeferredFoldColumns(projections)
		if err != nil || len(layout) != 1 || layout[0].name != "child_0_int_value" {
			t.Fatal("unobserved positions widened SQL rows", list, layout, err)
		}
		for _, projection := range projections {
			if row := projection.columns(layout); strings.Count(row, " AS ") != 2 || strings.Contains(row, "UNOBSERVED") {
				t.Fatal("sparse shape widened carrier", list, row)
			}
		}
	}
}

func TestNativeChildProjectionValidationIsBounded(t *testing.T) {
	body := "0"
	for depth := 1; depth <= 6; depth++ {
		body = `labels:items:map(p=>{"used":p[1]:type:len,"unused":[p[1],p[1]]}):foldl(` + body + `,(a,v)=>a+v.used)`
		p, _ := foldProjectionProgram(t, body+`>0`)
		ctx := &repeatCheckpointContext{Context: context.Background()}
		q, err := Build(ctx, p, WithDialect(PostgreSQL), WithBindings(BindJSON(hff.Path{"labels"}, Column("records", "labels"), JSON)))
		if err != nil || q == nil {
			t.Fatal(depth, err)
		}
		var count func(hff.Node) int
		count = func(n hff.Node) int {
			result := 1
			for i := 0; i < n.ChildCount(); i++ {
				child, _ := n.Child(i)
				result += count(child)
			}
			return result
		}
		nodes := count(p.Root())
		if ctx.checks > 1000+12*nodes*depth {
			t.Fatal("native child validation replay exceeded bounded workload", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}
