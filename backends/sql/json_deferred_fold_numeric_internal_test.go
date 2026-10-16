package sql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func TestNativeNumericFoldFamilyProof(t *testing.T) {
	b := builder{ctx: context.Background(), jsonValues: map[string]JSONFormat{"registered": JSON}}
	for _, tc := range []struct {
		name string
		typ  hff.Type
		kind hff.TypeKind
		code string
		want bool
	}{
		{"int", schema.IntType, hff.IntType, "source.value", true},
		{"float", schema.FloatType, hff.FloatType, "source.value", true},
		{"nullable int", schema.NullableType(schema.IntType), hff.IntType, "source.value", true},
		{"nullable float", schema.NullableType(schema.FloatType), hff.FloatType, "source.value", true},
		{"wider number", schema.Union(schema.IntType, schema.FloatType), hff.FloatType, "source.value", false},
		{"Any", schema.AnyType, hff.IntType, "source.value", false},
		{"Never", schema.NeverType, hff.IntType, "source.value", false},
		{"null", schema.NullType, hff.NullType, "NULL", false},
		{"mismatched float", schema.FloatType, hff.IntType, "source.value", false},
		{"mismatched int", schema.IntType, hff.FloatType, "source.value", false},
		{"registered JSON", schema.IntType, hff.IntType, "registered", false},
		{"bool", schema.BoolType, hff.BoolType, "source.value", false},
		{"string", schema.StringType, hff.StringType, "source.value", false},
	} {
		p := jsonParameter{typ: tc.typ, lossy: true, direct: &expression{tc.code, "source.present", tc.kind}}
		got, ok := b.nativeNumericDeferredFoldParameter(p)
		if ok != tc.want || ok && !reflect.DeepEqual(got, p) {
			t.Fatal(tc.name, ok, got)
		}
	}
	for _, p := range []jsonParameter{
		{typ: schema.IntType, format: JSON, direct: &expression{"source.value", "TRUE", hff.IntType}},
		{typ: schema.IntType, code: "source.value", format: JSON},
		{typ: schema.IntType, itemSlot: &jsonItemSlot{}, direct: &expression{"source.value", "TRUE", hff.IntType}},
		{typ: schema.IntType, deferred: &jsonDeferredValue{typ: schema.IntType, runtime: &jsonRuntimeValue{}}},
		{typ: schema.IntType, deferred: &jsonDeferredValue{typ: schema.IntType, choices: []jsonDeferredChoice{{"TRUE", deferredNull()}}}},
	} {
		if _, ok := b.nativeNumericDeferredFoldParameter(p); ok {
			t.Fatal("non-native descriptor admitted", p)
		}
	}
}

func TestNativeNumericFoldTransportKeepsKindAndScope(t *testing.T) {
	for _, kind := range []hff.TypeKind{hff.IntType, hff.FloatType} {
		typ := schema.NullableType(hff.PrimitiveType(kind))
		original := jsonParameter{typ: typ, lossy: true, direct: &expression{"source.value", "source.present", kind}}
		transport := jsonDeferredFoldTransport{native: &original}
		columns := transport.columns()
		if strings.Count(columns, "source.value") != 1 || !strings.Contains(columns, " AS int_value") || !strings.Contains(columns, " AS float_value") {
			t.Fatal("typed columns changed", columns)
		}
		b := builder{ctx: context.Background(), args: []any{"outer"}, steps: []string{"outer_step"}, jsonValues: map[string]JSONFormat{}}
		got, err := transport.decode(&b, hff.Node{}, "destination.value")
		if err != nil || got.direct == original.direct || got.direct.code != "destination.value" || got.direct.kind != kind || got.direct.present != "TRUE" || !got.typ.Equal(typ) || !got.lossy || got.format != "" {
			t.Fatal("source scope or native metadata changed", got, err)
		}
		if original.direct.code != "source.value" || original.direct.present != "source.present" || len(b.args) != 1 || len(b.steps) != 1 || len(b.jsonValues) != 0 {
			t.Fatal("decode mutated source or granted JSON proof")
		}
	}
}

func TestNativeNumericFoldRequiresEveryEmittedArm(t *testing.T) {
	p, fold := foldProjectionProgram(t, `labels:items:foldl(0,(a,v)=>a+1)>0`)
	b := builder{ctx: context.Background(), program: p}
	native := jsonDeferredArm{value: jsonParameter{typ: schema.IntType, direct: &expression{"source.int", "TRUE", hff.IntType}}}
	other := jsonDeferredArm{value: jsonParameter{typ: schema.AnyType, code: "source.json", format: JSON}}
	for _, tc := range []struct {
		arms []jsonDeferredArm
		want bool
	}{
		{nil, false},
		{[]jsonDeferredArm{native}, true},
		{[]jsonDeferredArm{native, other}, false},
		{[]jsonDeferredArm{other, native}, false},
		{[]jsonDeferredArm{native, {empty: true, value: other.value}}, true},
		{[]jsonDeferredArm{{empty: true, value: native.value}}, false},
	} {
		got, err := b.nativeNumericDeferredFoldInputs(fold, jsonDeferredList{arms: tc.arms})
		if err != nil || got != tc.want {
			t.Fatal(tc.arms, got, err)
		}
	}
	b.disableDeferredFoldPruning = true
	if got, err := b.nativeNumericDeferredFoldInputs(fold, jsonDeferredList{arms: []jsonDeferredArm{native}}); got || err != nil {
		t.Fatal("validation reentered native input transport", got, err)
	}
}

func TestNativeNumericFoldValidationIsBounded(t *testing.T) {
	body := "0.0"
	for depth := 1; depth <= 5; depth++ {
		body = `labels:items:map(p=>p[1]):flatmap(v=>[v:type:len,v:type:len/2]):foldl(` + body + `,(a,n)=>a+n)`
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
		if ctx.checks > 1000+8*nodes*depth {
			t.Fatal("native numeric validation replay exceeded bounded workload", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}
