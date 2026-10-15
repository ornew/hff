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

func runtimeMembershipSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	other := `other:items:map(p=>p[1])`
	fold := values + `:foldl([], (a,v)=>a:concat([v]))`
	return []string{
		fold + `:contains(null)`,
		fold + `:contains("YQ==")`,
		fold + `:map(v=>v:in("YQ==")):json=[true,false]:json`,
		fold + `:contains(other.app,other.z)`,
		fold + `[0]:in(other.app,null,other.z)`,
		fold + `:in(` + other + `,null)`,
		fold + `:unique`,
		fold + `:concat(` + fold + `):unique`,
		fold + `:concat(` + fold + `):distinct=` + fold + `:distinct`,
		fold + `:distinct:map(v=>v:type):json=` + fold + `:concat(` + fold + `):distinct:map(v=>v:type):json`,
		fold + `:distinct:map(v=>v:[string]:len):exists`,
		fold + `:contains(null,null!):json="null"`,
		fold + `[0]:in(other.app,null!):json="null"`,
		`other:items:any(p=>` + fold + `:contains(p[1],p[1]))`,
	}
}

func TestRuntimeMembershipBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeMembershipSources(tc) {
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

func runtimeDistinctPairSources(tc itemRoleFixture) []string {
	return []string{
		`labels:items:foldl([], (a,p)=>a:concat([p])):distinct[0][1]:type="` + tc.tag + `"`,
		`labels:items:foldr([], (p,a)=>[p]:concat(a)):distinct[-1][-1]:type="` + tc.tag + `"`,
	}
}

func TestRuntimeDistinctPreservesPairRoles(t *testing.T) {
	for _, tc := range itemRoleFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeDistinctPairSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func runtimeMembershipAnySources(itemRoleFixture) []string {
	fold := `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	first := fold + `[0]`
	distinct := first + `:[list[any]]:distinct`
	return []string{
		first + `:contains("absent",null!):json="null"`,
		first + `:contains(null,null!):json="null"`,
		first + `:contains(null!,null):json="null"`,
		first + `:contains(other.app,null!):json="null"`,
		first + `:contains("a","u"):exists`,
		first + `:in(other.app,null!):json="null"`,
		first + `:[list[any]]:unique`,
		distinct + `[0]:[int]:exists`,
		distinct + `[0]:[float]:exists`,
		distinct + `:map(v=>v:[int]:exists):json=[true]:json`,
		distinct + `:json="[-0.0]"`,
		distinct + `:contains(null)`,
		distinct + `:size=2`,
		distinct + `:size=0`,
		`other:items:any(p=>` + fold + `:map(p=>p:in(p)):all(v=>v))`,
	}
}

func runtimeMembershipEdgeFixture() itemRoleFixture {
	tc := runtimeComparisonEdgeFixture()
	object := func(v hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"app": v, "z": tc.second})
	}
	for _, pair := range [][2]hff.Value{
		{hff.String("auth"), hff.Int(1)},
		{hff.List(hff.Int(1), hff.Float(1)), hff.List(hff.Float(1), hff.Int(1))},
		{hff.List(hff.Float(math.Copysign(0, -1)), hff.Int(0)), hff.List(hff.Int(0), hff.Float(math.Copysign(0, -1)))},
		{hff.List(hff.Null(), hff.Null()), hff.List(hff.Null())},
		{hff.List(hff.Object(map[string]hff.Value{"a": hff.Null()}), hff.Object(nil), hff.Object(map[string]hff.Value{"a": hff.Null()})), hff.List()},
		{hff.List(hff.List(), hff.Object(nil), hff.List()), hff.List()},
	} {
		tc.extraPairs = append(tc.extraPairs, [2]hff.Value{object(pair[0]), object(pair[1])}, [2]hff.Value{object(pair[1]), object(pair[0])})
	}
	return tc
}

func TestRuntimeMembershipAnyBuild(t *testing.T) {
	for _, tc := range append(itemAnyFixtures(), runtimeMembershipEdgeFixture()) {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeMembershipAnySources(tc) {
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

func TestRuntimeMembershipNullNeedleDoesNotExpandTrees(t *testing.T) {
	tc := itemRoleFixtures()[2]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v])):contains(null)`,
		`labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))[0]:in(null)`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON)...))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(q.Where(), "FULL JOIN") {
			t.Fatal("null candidate expands a comparison tree")
		}
	}
}

func TestRuntimeMembershipPreservesNativeIn(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("values", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `values:foldl(0,(a,v)=>a+1):in(1,2)`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"values"}, hffsql.Column("records", "values"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), " IN (") || strings.Contains(q.Where(), "FULL JOIN") {
		t.Fatal("native scalar membership was replaced")
	}
}

func TestRuntimeMembershipCancellation(t *testing.T) {
	tc := runtimeMembershipEdgeFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{runtimeMembershipSources(tc)[0], runtimeMembershipSources(tc)[9]} {
		p, err := e.Compile(context.Background(), source)
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
				t.Fatalf("partial%v err%v", q, err)
			}
		}
	}
}

func TestRuntimeMembershipRetainsCapabilityChecks(t *testing.T) {
	tc := runtimeMembershipEdgeFixture()
	for _, id := range []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality} {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard().Without(id)))
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Compile(context.Background(), `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v])):distinct`)
		requireCode(t, err, hff.CapabilityError)
	}
}

func TestRuntimeMembershipRetainsFloatLoss(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Float("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	for _, tc := range []struct {
		suffix        string
		observesFloat bool
	}{
		{`:contains(labels.app)`, false},
		{`:unique`, false},
		{`:distinct:json:exists`, true},
		{`:distinct[0]:[float]:json:exists`, true},
		{`:distinct:map(v=>v:type):json:exists`, false},
	} {
		p, err := e.Compile(context.Background(), fold+tc.suffix)
		if err != nil {
			t.Fatal(tc.suffix, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for _, metadata := range []bool{false, true} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB && metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "labels_meta")))
				}
				binding := hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, options...)
				_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
				wantError := format == hffsql.JSONB && !metadata && tc.observesFloat
				if wantError {
					requireCode(t, err, hff.BackendError)
				} else if err != nil {
					t.Errorf("%s metadata=%t %s: %v", format, metadata, tc.suffix, err)
				}
			}
		}
	}
}
