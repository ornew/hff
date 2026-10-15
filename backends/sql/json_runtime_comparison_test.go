package sql_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func runtimeComparisonSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	other := `other:items:map(p=>p[1])`
	fold := values + `:foldl([], (a,v)=>a:concat([v]))`
	return []string{
		fold + `=` + values,
		values + `=` + fold,
		fold + `!=` + other,
		`{"values":` + fold + `}={"values":` + other + `}`,
		fold + `:map(v=>[v])=` + values + `:map(v=>[v])`,
		fold + `=null`,
		fold + `!=null`,
		fold + `!:map(v=>v=null):json=[false,false]:json`,
		fold + `:map(v=>v="auth"):json=` + values + `:map(v=>v="auth"):json`,
		fold + `:map(v=>"auth"!=v):json=` + values + `:map(v=>"auth"!=v):json`,
		fold + `:map(v=>(v="auth"):exists):json=` + values + `:map(v=>(v="auth"):exists):json`,
		fold + `:map(v=>v!):filter(v=>false)=[]`,
	}
}

func TestRuntimeComparisonBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeComparisonSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestRuntimeComparisonValidatesLeftBeforeRight(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("raw"), schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `[raw]=[labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v])),"\u0000"]`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"raw"}, hffsql.Column("records", "raw"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err == nil || !strings.Contains(err.Error(), "preserved numeric types") {
		t.Fatalf("left representation must fail before right NUL: %v", err)
	}
}

func runtimeScalarComparisonSources(tc itemRoleFixture) []string {
	fold := `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	var sources []string
	for _, op := range []string{"=", "!=", "<", "<=", ">", ">="} {
		comparison := fold + `[0]` + op + `other.app`
		sources = append(sources, comparison, `(`+comparison+`):exists`)
	}
	for _, op := range []string{"=", "!=", "<", "<=", ">", ">="} {
		if tc.tag != "bytes" || op == "=" || op == "!=" {
			sources = append(sources, fold+`[-1]`+op+`other.z`)
		}
	}
	return append(sources,
		`{"value":`+fold+`[0]}={"value":other.app}`,
		fold+`[-1]=other.app`,
		`(`+fold+`[-1]!=other.app):exists`,
		fold+`[0]!=other.app!`,
		`other:items:any(p=>`+fold+`:map(v=>v=p[1]):json:exists)`,
	)
}

func runtimeComparisonEdgeFixture() itemRoleFixture {
	tc := itemAnyFixtures()[0]
	tc.name = "edges/any-field"
	object := func(v hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"app": v, "z": tc.second})
	}
	for _, pair := range [][2]hff.Value{
		{hff.Int(9007199254740993), hff.Float(9007199254740992)},
		{hff.Int(math.MaxInt64), hff.Float(9223372036854775808)},
		{hff.Int(math.MinInt64), hff.Float(-9223372036854775808)},
		{hff.Float(1.5), hff.Int(1)},
		{hff.Float(math.SmallestNonzeroFloat64), hff.Int(0)},
		{hff.Float(math.MaxFloat64), hff.Int(math.MaxInt64)},
		{hff.Float(math.Copysign(0, -1)), hff.Float(0)},
		{hff.Object(map[string]hff.Value{"a": hff.Null()}), hff.Object(nil)},
		{hff.List(hff.Null()), hff.List()},
		{hff.List(hff.Int(1), hff.Float(0)), hff.List(hff.Float(1), hff.Int(0))},
		{hff.Object(map[string]hff.Value{"0": hff.List()}), hff.List(hff.Object(nil))},
		{hff.String("YQ=="), hff.String("YQ==")},
		{hff.Bool(false), hff.Int(0)},
	} {
		tc.extraPairs = append(tc.extraPairs, [2]hff.Value{object(pair[0]), object(pair[1])}, [2]hff.Value{object(pair[1]), object(pair[0])})
	}
	return tc
}

func TestRuntimeComparisonRetainsCapabilityChecks(t *testing.T) {
	tc := runtimeComparisonEdgeFixture()
	for _, id := range []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality} {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard().Without(id)))
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Compile(context.Background(), runtimeScalarComparisonSources(tc)[0])
		requireCode(t, err, hff.CapabilityError)
	}
}

func TestRuntimeScalarComparisonBuild(t *testing.T) {
	for _, tc := range append(itemAnyFixtures(), runtimeComparisonEdgeFixture()) {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeScalarComparisonSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestRuntimeComparisonRejectsStaticBytesOrdering(t *testing.T) {
	tc := runtimeComparisonEdgeFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Compile(context.Background(), `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))[-1]<other.z`)
	requireCode(t, err, hff.TypeError)
}

func TestRuntimeComparisonCancellation(t *testing.T) {
	tc := runtimeComparisonEdgeFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), runtimeComparisonSources(tc)[0])
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, true)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		probe := &buildCancellationContext{Context: ctx, after: after, cancel: cancel}
		q, err := hffsql.Build(probe, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("checkpoint%d: partial%v err%v", after, q, err)
		}
	}
}

func TestRuntimeComparisonPreservesOrdinaryFallback(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("raw"), schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `raw=labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v])):type`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"raw"}, hffsql.Column("records", "raw"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
}
