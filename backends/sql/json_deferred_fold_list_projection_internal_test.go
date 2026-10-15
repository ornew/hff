package sql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func TestFixedFoldIndexObservationUsesLexicalBindings(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []float64
	}{
		{`a+v[0]:string:len`, []float64{0}},
		{`a+v[-1]:string:len+v[1.0]:string:len`, []float64{-1, 1}},
		{`[1]:foldl(a,(b,x)=>b+v[0]:string:len)`, []float64{0}},
		{`a+v[0]:string:len+[1]:foldl(0,(b,v)=>b+v)`, []float64{0}},
		{`a+v[0]!:string:len`, []float64{0}},
		{`a+v[0]:string:len+v:size`, nil},
		{`a+v![0]:string:len`, nil},
		{`a+v[v:size-1]:string:len`, nil},
		{`a+v[0.5]:string:len`, nil},
		{`a+v[0]:string:len+[1]:map(x=>v:type):size`, nil},
	} {
		p, fold := foldProjectionProgram(t, `labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>`+tc.body+`)>0`)
		b := builder{ctx: context.Background(), program: p}
		got, err := b.observedDeferredFoldIndexes(fold, "foldl")
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.body, got, tc.want, err)
		}
		b.disableDeferredFoldPruning = true
		if got, err := b.observedDeferredFoldIndexes(fold, "foldl"); got != nil || err != nil {
			t.Fatal("validation reentered index projection", got, err)
		}
	}
}

func TestFixedFoldPositionsRequireLiteralScope(t *testing.T) {
	b := builder{ctx: context.Background()}
	leaf := jsonParameter{typ: schema.StringType, code: "outer.value", format: JSON}
	for _, tc := range []struct {
		name string
		list *jsonDeferredList
		want bool
	}{
		{"literal", &jsonDeferredList{literal: true, arms: []jsonDeferredArm{{selected: "TRUE", value: leaf}}}, true},
		{"repeated", &jsonDeferredList{literalPositions: []int{0, 0}, arms: []jsonDeferredArm{{selected: "TRUE", value: leaf}}}, true},
		{"empty sentinel", &jsonDeferredList{literal: true, arms: []jsonDeferredArm{{empty: true}, {selected: "TRUE", value: leaf}}}, true},
		{"variable", &jsonDeferredList{arms: []jsonDeferredArm{{selected: "TRUE", value: leaf}}}, false},
		{"filtered", &jsonDeferredList{literal: true, arms: []jsonDeferredArm{{selected: "FALSE", value: leaf}}}, false},
		{"scoped step", &jsonDeferredList{literal: true, arms: []jsonDeferredArm{{selected: "TRUE", value: leaf, steps: []string{"local"}}}}, false},
		{"unmapped row", &jsonDeferredList{literalPositions: []int{0}, arms: []jsonDeferredArm{{selected: "TRUE", value: leaf}, {selected: "TRUE", value: leaf}}}, false},
		{"empty", &jsonDeferredList{literal: true}, false},
	} {
		_, got, err := b.fixedDeferredFoldPositions(&jsonDeferredValue{typ: hff.NewListType(schema.StringType), list: tc.list})
		if got != tc.want || err != nil {
			t.Fatal(tc.name, got, err)
		}
	}
	if _, ok, err := b.fixedDeferredFoldPositions(deferredLeaf(jsonParameter{typ: hff.NewListType(schema.NeverType), code: "runtime.value", format: JSON})); ok || err != nil {
		t.Fatal("element schema used as cardinality proof", ok, err)
	}
}

func TestFixedFoldPacketKeepsMultiplicityAndScope(t *testing.T) {
	p, _ := foldProjectionProgram(t, `labels:items:foldl(0,(a,p)=>a+1)>0`)
	kept := jsonParameter{typ: schema.BytesType, code: "source.payload", format: JSON, lossy: true}
	discarded := jsonParameter{typ: schema.StringType, code: "UNOBSERVED_SOURCE", format: JSON}
	positions := []int{0, 1, 0}
	typ := hff.NewListType(schema.Union(schema.BytesType, schema.StringType))
	list := &jsonDeferredList{null: "source.null", present: "source.present", literalPositions: positions, arms: []jsonDeferredArm{{selected: "TRUE", value: kept}, {selected: "TRUE", value: discarded}}}
	original := &jsonDeferredValue{typ: typ, present: "source.parent", list: list}
	outer := map[hff.BindingID]jsonParameter{777: {typ: schema.StringType, code: "outer.value", format: JSON}}
	snapshot := copyJSONParameters(outer)
	b := builder{ctx: context.Background(), program: p, parameters: outer, args: []any{"outer"}, steps: []string{"outer_step"}}
	packet, err := b.packFixedDeferredFoldList(p.Root(), original, positions, []float64{0, -1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(packet.code, "source.payload") != 1 || strings.Contains(packet.code, "UNOBSERVED_SOURCE") || strings.Contains(packet.code, "json_agg") || !packet.typ.Equal(typ) {
		t.Fatal("selected child duplicated or row transport retained", packet.code)
	}
	decoded, err := packet.decode(&b, p.Root(), "destination.value")
	if err != nil || !decoded.typ.Equal(typ) || len(decoded.choices) != 2 {
		t.Fatal("nullable literal exposed without a branch", err)
	}
	literal := decoded.choices[0].value.list
	if literal == nil || !literal.literal || literal.null != "FALSE" || len(literal.arms) != 3 {
		t.Fatal("original semantic positions changed", literal)
	}
	first := deferredLeaf(literal.arms[0].value)
	if !reflect.DeepEqual(literal.arms[0].value, literal.arms[2].value) || !first.leaf.lossy || !first.typ.Equal(kept.typ) || !literal.arms[1].value.typ.Equal(discarded.typ) {
		t.Fatal("repeated identity, type or loss metadata changed")
	}
	if strings.Contains(first.leaf.code, "source.") {
		t.Fatal("decoder captured source alias", first.leaf.code)
	}
	if !reflect.DeepEqual(b.parameters, snapshot) || b.args[0] != "outer" || b.steps[0] != "outer_step" || len(b.steps) != 1 {
		t.Fatal("enclosing state changed")
	}
	if original.list != list || original.present != "source.parent" || !reflect.DeepEqual(positions, []int{0, 1, 0}) || list.literal {
		t.Fatal("source descriptor mutated")
	}
	if null := decoded.choices[1].value; null.leaf == nil || null.leaf.direct == nil || null.leaf.direct.kind != hff.NullType {
		t.Fatal("parent null lost")
	}
}

func TestFixedFoldSelectedPacketOwnsNewProjectionSteps(t *testing.T) {
	p, _ := foldProjectionProgram(t, `labels:items:foldl(0,(a,p)=>a+1)>0`)
	selected := &jsonDeferredValue{typ: schema.NullableType(schema.BytesType), present: "TRUE", selection: &jsonDeferredSelection{
		arms:     []jsonDeferredArm{{from: "(VALUES(1)) AS chosen(n)", selected: "outer.keep", value: jsonParameter{typ: schema.BytesType, code: "outer.payload", format: JSON}}},
		fallback: deferredNull(),
	}}
	child := deferredValueParameter(selected)
	original := &jsonDeferredValue{typ: hff.NewListType(child.typ), present: "TRUE", list: &jsonDeferredList{literal: true, null: "FALSE", present: "TRUE", arms: []jsonDeferredArm{{selected: "TRUE", value: child}}}}
	b := builder{ctx: context.Background(), program: p, steps: []string{"outer_step"}}
	packet, err := b.packFixedDeferredFoldList(p.Root(), original, []int{0}, []float64{0})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.steps) != 2 || b.steps[0] != "outer_step" || strings.Count(strings.Join(b.steps, " "), "outer.payload") != 1 || !strings.Contains(b.steps[1], "outer.keep") {
		t.Fatal("selected projection was lost, duplicated, or moved outside its owning scope", b.steps)
	}
	step := b.steps[1]
	alias := step[strings.LastIndex(step, " AS ")+4:]
	if !strings.Contains(packet.code, alias+".v") {
		t.Fatal("packet does not reference emitted child projection", packet.code, step)
	}
}

func TestNestedFixedFoldProjectionValidationIsBounded(t *testing.T) {
	body := "0"
	for depth := 1; depth <= 5; depth++ {
		body = `labels:items:map(p=>[p[1],{"unused":[p[1],p[1]]}]):foldl(` + body + `,(a,v)=>a+v[0]:string:len)`
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
				c, _ := n.Child(i)
				result += count(c)
			}
			return result
		}
		nodes := count(p.Root())
		if ctx.checks > 500+6*nodes*depth {
			t.Fatal("fixed fold projection validation replay exceeded bounded workload", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}
