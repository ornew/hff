package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func deferredFindSources(tc itemRoleFixture) []string {
	const values = `labels:items:map(p=>p[1])`
	const fold = values + `:foldl([], (a,v)=>a:concat([v]))`
	return []string{
		fold + `:find(v=>true):type=` + fold + `[0]:type`,
		fold + `:find(v=>false):type="null"`,
		fold + `:find(v=>v):exists`,
		fold + `:find(v=>v!):type=` + fold + `:filter(v=>v:exists)[0]:type`,
		fold + `:find(v=>v:[string]):json:exists`,
		fold + `:find(v=>v:[null]:type="null"):json="null"`,
		fold + `:find(v=>v:type="` + tc.tag + `"):type="` + tc.tag + `"`,
		`[` + fold + `:find(v=>v:exists)]:map(v=>v:type):json=[` + fold + `:find(v=>v:exists):type]:json`,
		fold + `:find(v=>true):[string]:len>0`,
		fold + `:map(v=>{"value":v}):find(v=>v.value:exists).value:type="` + tc.tag + `"`,
		fold + `:map(v=>[v]):find(v=>true)[0]:type=` + fold + `[0]:type`,
		fold + `:map(v=>v!):find(v=>true):json:exists`,
		values + `:find(v=>true):type=` + fold + `:find(v=>true):type`,
		`other:items:any(p=>` + fold + `:find(v=>v:type=p[1]:type):type=p[1]:type)`,
		`other:items:any(p=>` + fold + `:find(p=>p:type="` + tc.tag + `"):type="` + tc.tag + `" p[0]="app")`,
		fold + `:map(v=>[v]):find(v=>true):map(v=>v:type):json=[` + fold + `[0]:type]:json`,
		fold + `:filter(v=>false):find(v=>true):exists`,
		`labels:items:foldl([], (a,p)=>a:concat([p])):find(p=>p[0]="z")[1]:type="` + tc.tag + `"`,
	}
}

func deferredFindAnySources(itemRoleFixture) []string {
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	const items = fold + `[0]:[list[any]]`
	const first = items + `:find(v=>true)`
	return []string{
		first + `:type=` + items + `[0]:type`,
		first + `:json=` + items + `[0]:json`,
		first + `:type="int"`,
		first + `:type="float"`,
		first + `:json="-0.0"`,
		first + `:json="0"`,
		first + `:json="1.0"`,
		items + `:find(v=>v:[number]):exists`,
		items + `:find(v=>v:[null]:type="null"):json="null"`,
		items + `:find(v=>v!):exists`,
		items + `:map(v=>v!):find(v=>true):json:exists`,
		first + `:[list[any]]:map(v=>v:type):json:exists`,
		`other:items:any(p=>` + items + `:find(v=>v:in(p[1])):type=p[1]:type)`,
		fold + `[0]:find(v=>false):json="null"`,
	}
}

func TestDeferredFindAnyBuild(t *testing.T) {
	for _, tc := range append(itemAnyFixtures(), runtimeMembershipEdgeFixture()) {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredFindAnySources(tc) {
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

func TestDeferredFindRetainsFloatLoss(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Float("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	for _, tc := range []struct {
		suffix        string
		observesFloat bool
	}{
		{`:find(v=>true):json:exists`, true},
		{`:find(v=>true):type="float"`, false},
		{`:find(v=>v:[bytes]:exists):[bytes]:string:exists`, false},
		{`:find(v=>false):json="null"`, true},
	} {
		p, err := e.Compile(context.Background(), fold+tc.suffix)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for _, metadata := range []bool{false, true} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB && metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
				}
				_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, options...)))
				if format == hffsql.JSONB && !metadata && tc.observesFloat {
					requireCode(t, err, hff.BackendError)
				} else if err != nil {
					t.Fatal(tc.suffix, format, metadata, err)
				}
			}
		}
	}
}

func TestDeferredFindRetainsBuildDiagnostics(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	for _, tc := range []struct {
		source   string
		rejected bool
	}{
		{fold + `:filter(v=>false):find(v=>v:matches(".")):exists`, true},
		{fold + `:find(v=>v:concat("\u0000")):exists`, true},
		{`labels:items:foldl(null,(a,p)=>null):find(v=>v:[string]:matches(".")):exists`, false},
		{`labels:items:foldl(null,(a,p)=>null):find(v=>labels:keys:string:matches(".")):exists`, false},
	} {
		t.Log(tc.source)
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("labels"), hffsql.JSON)))
		if tc.rejected {
			requireCode(t, err, hff.BackendError)
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeferredFindCancellation(t *testing.T) {
	tc := runtimeMembershipEdgeFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), deferredFindSources(tc)[9])
	if err != nil {
		t.Fatal(err)
	}
	opts := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, true)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, opts...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, opts...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("partial %v, error %v", q, err)
		}
	}
}

func TestDeferredFindKeepsNativeStoredPath(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("values", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `values:find(v=>v>0)>0`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"values"}, hffsql.Column("values"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), "LIMIT 1") || strings.Contains(q.Where(), " AS arm") {
		t.Fatal("ordinary stored find acquired deferred arm selection")
	}
}

func TestDeferredFindBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredFindSources(tc) {
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
