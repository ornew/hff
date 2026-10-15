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

const knownNoValueFoldSuffix = `:foldl(null,(a,v)=>a!+1):type="null"`

func unusedInputFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	var sources []string
	for _, receiver := range []string{values, values + `:map(v=>[v,v])`, values + `:map(v=>{"payload":v,"literal":"PACKET_ONLY","nested":[v,v]})`} {
		sources = append(sources,
			receiver+`:foldl(0,(a,v)=>a+1)=labels:size`,
			receiver+`:foldr(0,(v,a)=>a+1)=labels:size`,
			receiver+`:foldl("",(a,v)=>a:concat("x")):len=labels:size`,
			receiver+`:foldl(null,(a,v)=>a!+1):type="null"`,
		)
	}
	sources = append(sources,
		values+`:map(v=>[v,v]):foldl(0,(a,v)=>[1,2]:foldl(a,(b,v)=>b+v))=labels:size*3`,
		values+`:map(v=>[v,v]):foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v:size))=labels:size*2`,
		values+`:map(v=>[v,v]):foldl(0,(a,v)=>a+labels:size)=labels:size*labels:size`,
		values+`:map(v=>v!):map(v=>[v]):foldl(0,(a,v)=>a+1):exists`,
		values+`:map(v=>v!):flatmap(v=>[]):foldl("seed",(a,v)=>"body"):type="null"`,
		values+`:flatmap(v=>[]):foldl("seed",(a,v)=>"body")="seed"`,
		values+`:flatmap(v=>null):foldl(null!,(a,v)=>1):type="null"`,
		values+`:filter(v=>false):foldl("seed",(a,v)=>"body")="seed"`,
		values+`:map(v=>[v]):foldr(null!,(v,a)=>1):exists`,
		values+`:map(v=>[v]):foldl(null,(a,v)=>a!+1):type="null"`,
	)
	return sources
}

func TestUnusedDeferredInputFoldsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range unusedInputFoldSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Fatal(tc.name, format, source, err)
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}

func TestUnusedDeferredFoldKnownNoValueArgumentBoundary(t *testing.T) {
	e := unusedFoldEngine(t)
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1])`+knownNoValueFoldSuffix)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	requireConcatArgsUsed(t, q)
}

func unusedFoldEngine(t *testing.T, methods ...hff.MethodDeclaration) *engine.Engine {
	t.Helper()
	caps := capabilities.Standard()
	for _, method := range methods {
		caps = caps.Allow(capabilities.Method(method.ID()))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("labels", schema.Struct(schema.Bytes("asset"), schema.String("name")))))), engine.WithCapabilities(caps), engine.WithMethods(methods...))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestUnusedDeferredInputFoldKeepsRecursionWithoutPayload(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, direction := range []string{"foldl(0,(a,v)=>a+1)", "foldr(0,(v,a)=>a+1)"} {
		source := `labels:items:map(p=>[p[1],p[1]]):` + direction + `>0`
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(q.Where(), "WITH RECURSIVE") || !strings.Contains(q.Where(), "'null'::json AS value") || strings.Count(q.Where(), "json_build_array(") != 1 {
			t.Fatal("unused item retained its packet or lost recursive state", q.Where())
		}
		requireConcatArgsUsed(t, q)
	}
}

func TestUnusedDeferredInputFoldKeepsDiagnostics(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range []string{
		`labels:items:map(p=>{"bad":"\u0000SOURCE","value":p[1]}):foldl("\u0000SEED",(a,v)=>"\u0000BODY"):exists`,
		`labels:items:map(p=>[p[1]]):foldl("\u0000SEED",(a,v)=>"\u0000BODY"):exists`,
		`labels:items:map(p=>[p[1]]):foldl("",(a,v)=>"\u0000BODY"):exists`,
		`labels:items:map(p=>[p[1]]):filter(v=>false):foldl("",(a,v)=>"\u0000BODY"):exists`,
		`labels:items:map(p=>[p[1]]):flatmap(v=>null):foldl("",(a,v)=>"\u0000BODY"):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		var detail *hff.Error
		first := strings.Index(source, `"\u0000`)
		if !errors.As(err, &detail) || detail.Code != hff.BackendError || detail.Span == nil || detail.Span.Start != first {
			t.Fatal("changed receiver/seed/callback diagnostic order", source, err, first)
		}
	}
}

func TestUnusedDeferredInputFoldKeepsExtensionVisits(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.fold.touch", Name: "touch", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for _, source := range []string{
		`labels:items:map(p=>[p[1],1:touch]):foldl(0,(a,v)=>a+1)>0`,
		`labels:items:map(p=>[p[1]]):foldl(0:touch,(a,v)=>a+1)>0`,
		`labels:items:map(p=>[p[1]]):foldl(0,(a,v)=>(a+1):touch)>0`,
	} {
		count := 0
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(call *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			count++
			return call.Expression(schema.IntType, "{}", receiver)
		})))
		if err != nil {
			t.Fatal(source, err)
		}
		if count != 1 {
			t.Fatal("extension invocation count changed", count, source)
		}
		requireConcatArgsUsed(t, q)
	}
}

func TestUnusedDeferredInputFoldCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range []string{
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+1)>0`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>[1,2]:foldl(a,(b,v)=>b+v))>0`,
		`labels:items:map(p=>[p[1]!]):foldr(null!,(v,a)=>1):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))}
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, options...); err != nil {
			t.Fatal(err)
		}
		for after := int64(1); after <= baseline.checks.Load(); after++ {
			base, cancel := context.WithCancel(context.Background())
			ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
			q, err := hffsql.Build(ctx, p, options...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("partial query escaped cancellation", source, after, q, err)
			}
		}
	}
}
