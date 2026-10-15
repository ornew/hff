package sql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func runtimeFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	types := values + `:map(v=>v:type):json`
	lastType := values + `:map(v=>v:type)[-1]`
	return []string{
		values + `:foldl([], (acc,v)=>acc:concat([v])):map(v=>v:type):json=` + types,
		values + `:foldr([], (v,acc)=>[v]:concat(acc)):map(v=>v:type):json=` + types,
		values + `:foldl(null,(acc,v)=>v):type=` + lastType,
		values + `:foldl({},(acc,v)=>{"previous":acc,"value":v}).value:type=` + lastType,
		values + `:foldl(null,(acc,v)=>[acc,v])[1]:type=` + lastType,
		values + `:foldl([], (acc,v)=>acc:concat([v]))[-1]:type=` + lastType,
		values + `:foldl([], (acc,v)=>acc:concat([v])):map(v=>v:[string]:len):exists`,
		values + `:foldl([], (acc,v)=>acc:concat([v])):map(v=>v!):filter(v=>false):exists`,
		values + `:foldl([], (acc,v)=>acc:concat([v])):map(v=>v:json):json=` + values + `:map(v=>v:json):json`,
		// A tagged seed exercises runtime state even for JSON-native inputs.
		values + `:foldl(["YQ==":frombase64], (acc,v)=>acc:concat([v])):map(v=>v:[bytes]:base64):exists`,
		`other:items:any(p=>` + values + `:foldl([], (acc,v)=>acc:concat([{"key":p[0],"value":v}])):map(o=>o.value:type):json=` + types + `)`,
	}
}

func runtimeNestedFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	retained := values + `:foldl([], (acc,v)=>acc:concat([v]))`
	types := values + `:map(v=>v:type):json`
	return []string{
		retained + `:map(v=>[v]:foldl("",(s,x)=>s:concat(x:type))):json=` + types,
		retained + `:map(v=>[{"value":v}]:foldr("",(x,s)=>x.value:type:concat(s))):json=` + types,
		values + `:foldl([], (a,v)=>a:concat([v]):foldl([], (b,x)=>b:concat([x]))):map(v=>v:type):json=` + types,
	}
}

func TestRuntimeFoldCancellation(t *testing.T) {
	tc := deferredValueFixtures()[2]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), runtimeFoldSources(tc)[0])
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, tc.metadata)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
		q, err := hffsql.Build(ctx, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("checkpoint%d partial%v err%v", after, q, err)
		}
	}
}

func TestRuntimeFoldRetainsFloatLoss(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Float("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const fold = `labels:items:map(p=>p[1]):foldl([], (acc,v)=>acc:concat([v]))`
	for _, suffix := range []string{`:type="list"`, `:json:exists`, `:map(v=>v:type):json:exists`, `:[list[bytes]]:json:exists`} {
		p, err := e.Compile(context.Background(), fold+suffix)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			wantError := format == hffsql.JSONB && suffix == `:json:exists`
			if (err != nil) != wantError {
				t.Errorf("%s %s: %v", format, suffix, err)
			}
		}
	}
}

func TestRuntimeFoldBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range append(runtimeFoldSources(tc), runtimeNestedFoldSources(tc)...) {
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
