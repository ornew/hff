package local_test

import (
	"context"
	"math"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestCollectionMethods(t *testing.T) {
	obj := hff.Object(map[string]hff.Value{"b": hff.Int(2), "a": hff.Null()})
	cases := []struct {
		query string
		value hff.Value
		want  bool
	}{
		{`x:size()=0`, hff.List(), true},
		{`x:size()=2`, obj, true},
		{`x:size()=null`, hff.Int(1), true},
		{`x:has(-1)`, hff.List(hff.Null()), true},
		{`x:has(1.0)`, hff.List(hff.Int(0), hff.Int(1)), true},
		{`x:has(1)`, hff.List(hff.Null()), false},
		{`x:has(0.5)=null`, hff.List(hff.Null()), true},
		{`x:has(-9223372036854775808)`, hff.List(hff.Int(1)), false},
		{`x:has("a")`, obj, true},
		{`x:has("missing")`, obj, false},
		{`x:get("a")=null`, obj, true},
		{`x:get("missing")=null`, obj, true},
		{`x:get("b")=2`, obj, true},
		{`x:contains(null,1)`, hff.List(hff.Null(), hff.Float(1)), true},
		{`x:contains(1,2)`, hff.List(hff.Int(1)), false},
		{`x:contains({"a":null})`, hff.List(obj), false},
		{`x:contains([1,null])`, hff.List(hff.List(hff.Float(1), hff.Null())), true},
		{`x:unique()`, hff.List(), true},
		{`x:unique()`, hff.List(hff.Int(1), hff.Float(1)), false},
		{`x:unique()`, hff.List(hff.Null(), hff.Null()), false},
		{`x:unique()`, hff.List(hff.Int(1), hff.String("1")), true},
		{`x:concat([null,2])=[1,null,2]`, hff.List(hff.Int(1)), true},
		{`x:concat([])=[1]`, hff.List(hff.Int(1)), true},
		{`x:sort()=[]`, hff.List(), true},
		{`x:sort()=[1,2,3]`, hff.List(hff.Int(3), hff.Float(1), hff.Int(2)), true},
		{`x:sort()[0]:[float]=1`, hff.List(hff.Float(1), hff.Int(1)), true},
		{`x:sort()[1]:[int]=1`, hff.List(hff.Float(1), hff.Int(1)), true},
		{`x:sort()=[9007199254740992.0,9007199254740993]`, hff.List(hff.Int(9007199254740993), hff.Float(9007199254740992)), true},
		{`x:sort()=null`, hff.List(hff.Null()), true},
		{`x:sort()=null`, hff.List(hff.Bool(true)), true},
		{`x:sort()=null`, hff.List(hff.Int(1), hff.String("a")), true},
		{`x:keys()=["a","b"]`, obj, true},
		{`x:values()=[null,2]`, obj, true},
		{`x:items()=[["a",null],["b",2]]`, obj, true},
		{`x:keys()=[]`, hff.Object(nil), true},
		{`x!:keys()=null`, hff.Null(), false},
		{`x:size()=null`, hff.Null(), true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			f := build(t, tc.query)
			got, err := f.Match(context.Background(), record(tc.value, hff.Null()))
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestCollectionLimitsAndOwnership(t *testing.T) {
	ctx := context.Background()
	f := build(t, `x:concat([1])`, local.WithLimits(hff.Limits{MaxGeneratedValues: 3}))
	_, err := f.Match(ctx, record(hff.List(hff.Int(0)), hff.Null()))
	code(t, err, hff.LimitError)
	f = build(t, `x:sort()`, local.WithLimits(hff.Limits{MaxSteps: 20}))
	_, err = f.Match(ctx, record(hff.List(hff.Int(4), hff.Int(3), hff.Int(2), hff.Int(1)), hff.Null()))
	code(t, err, hff.LimitError)
	f = build(t, `x:items()`, local.WithLimits(hff.Limits{MaxGeneratedValues: 4}))
	_, err = f.Match(ctx, record(hff.Object(map[string]hff.Value{"a": hff.Null()}), hff.Null()))
	code(t, err, hff.LimitError)
	original := hff.List(hff.Int(2), hff.Int(1))
	f = build(t, `x:sort()=[1,2] x=[2,1]`)
	got, err := f.Match(ctx, record(original, hff.Null()))
	if err != nil || !got {
		t.Fatal(got, err)
	}
	// Wide numeric values retain their exact ordering and original subtypes.
	f = build(t, `x:sort()[0]:[int]=-9223372036854775808`)
	got, err = f.Match(ctx, record(hff.List(hff.Float(0x1p63), hff.Int(math.MinInt64)), hff.Null()))
	if err != nil || !got {
		t.Fatal(got, err)
	}
}

func TestDynamicCollectionResultTypes(t *testing.T) {
	e := configured(t)
	any := hff.PrimitiveType(hff.AnyType)
	list := hff.NewListType(any)
	nullable := func(v hff.Type) hff.Type { return hff.NewUnionType(v, hff.PrimitiveType(hff.NullType)) }
	cases := []struct {
		source string
		typ    hff.Type
	}{
		{`x:concat([])`, nullable(list)},
		{`x:sort()`, nullable(list)},
		{`x:values()`, nullable(list)},
		{`x:items()`, nullable(hff.NewListType(list))},
		{`x:keys()`, nullable(hff.NewListType(hff.PrimitiveType(hff.StringType)))},
		{`x:get("a")`, any},
	}
	for _, tc := range cases {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		call, ok := p.Root().Child(0)
		if !ok || !call.Type().Equal(tc.typ) {
			t.Fatalf("%s: got %v want %v", tc.source, call.Type(), tc.typ)
		}
	}
	// A schema union containing Any must retain unknown element types.
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("x", schema.IntType), schema.Field("y", schema.Union(schema.AnyType, schema.StringType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `x:concat(y)`)
	if err != nil {
		t.Fatal(err)
	}
	call, _ := p.Root().Child(0)
	if !call.Type().Equal(nullable(list)) {
		t.Fatalf("union Any lost its possible element types: %v", call.Type())
	}
}
