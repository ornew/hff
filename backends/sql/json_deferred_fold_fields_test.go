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

func observedFoldFieldSources(itemRoleFixture) []string {
	input := `labels:items:map(p=>{"used":p[1],"unused":{"payload":[p[1],p[1]],"literal":"PACKET_ONLY"}})`
	sources := []string{
		input + `:foldl(0,(a,v)=>a+v.used:string:len)>5`,
		input + `:foldl("",(a,v)=>a:concat(v.used:type)):len>10`,
		input + `:foldr(0,(v,a)=>a+v.used:string:len)>5`,
		input + `:foldl(0,(a,v)=>a+v["used"]:string:len)>5`,
		input + `:foldl(0,(a,v)=>a+v.used!:string:len)>5`,
		input + `:foldl(true,(a,v)=>a v["missing"]=null)`,
		input + `:foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v.used:string:len))>5`,
		input + `:foldl(0,(a,v)=>a+v.used:string:len+[1]:foldl(0,(b,v)=>b+v))>5`,
		`labels:items:map(p=>{"used":[p[1],p[1]],"unused":{"a":p[1],"b":[p[1],p[1]]}}):foldl(0,(a,v)=>a+v.used:size)>2`,
		`labels:items:map(p=>{"used":[p[1],p[1]],"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.used[0]:type)):len>10`,
		`labels:items:map(p=>{"used":{"a":p[1],"b":[p[1],p[1]]},"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.used.a:string:len)>5`,
		`labels:items:map(p=>{"used":{"a":p[1]},"unused":[p[1],p[1]]}):foldl(true,(a,v)=>a v.used["missing"]=null)`,
		`labels:items:map(p=>{"a":p[1],"b":[p[1]],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.a:string:len+v.b:size)>5`,
		`labels:items:map(p=>{"":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v[""]:string:len)>5`,
		`labels:items:map(p=>{"日本.語":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v["日本.語"]:string:len)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":p[1]!}):foldl(0,(a,v)=>a+v.used:string:len):exists`,
		`labels:items:map(p=>{"used":p[1],"unused":null!}):foldl(0,(a,v)=>a+v.used:string:len):exists`,
		`labels:items:filter(p=>false):map(p=>{"used":p[1],"unused":[p[1]]}):foldl(0,(a,v)=>a+v.used:string:len)=0`,
	}
	return sources
}

func TestDeferredFoldObservedObjectFieldsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e := itemFieldFoldEngine(t, tc)
		for _, source := range observedFoldFieldSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Fatal(tc.name, source, format, err)
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}

func itemFieldFoldEngine(t *testing.T, tc itemRoleFixture) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDeferredFoldObjectProjectionRetainsRecursion(t *testing.T) {
	e := unusedFoldEngine(t)
	source := `labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.used:string:len)>5`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), "WITH RECURSIVE") || strings.Count(q.Where(), "json_build_array(") != 2 {
		t.Fatal("projection lost the recursive fold or its unobserved field placeholder", q.Where())
	}
	requireConcatArgsUsed(t, q)
}

func TestDeferredFoldObjectFieldsKeepCustomTranslators(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.fields.touch", Name: "touch", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for _, source := range []string{
		`labels:items:map(p=>{"used":p[1],"unused":1:touch}):foldl(0,(a,v)=>a+v.used:string:len)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1]]}):foldl(0:touch,(a,v)=>a+v.used:string:len)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1]]}):foldl(0,(a,v)=>(a+v.used:string:len):touch)>5`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(call *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return call.Expression(schema.IntType, "{}", receiver)
		})))
		if err != nil || calls != 1 {
			t.Fatal(source, calls, err)
		}
		requireConcatArgsUsed(t, q)
	}
}

func TestDeferredFoldObjectFieldsKeepDiagnosticOrder(t *testing.T) {
	e := unusedFoldEngine(t)
	input := `labels:items:map(p=>{"used":p[1],"unused":[p[1]]})`
	for _, source := range []string{
		`labels:items:map(p=>{"used":p[1],"unused":"\u0000"}):foldl(0,(a,v)=>a+v.used:string:len)>5`,
		input + `:foldl("\u0000",(a,v)=>a:concat(v.used:string))="body"`,
		input + `:foldl("seed",(a,v)=>a:concat(v.used:string):concat("\u0000"))="body"`,
		`labels:items:filter(p=>false):map(p=>{"used":p[1],"unused":"\u0000"}):foldl(0,(a,v)=>a+v.used:string:len)>5`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		requireCode(t, err, hff.BackendError)
		var diagnostic *hff.Error
		if q != nil || !errors.As(err, &diagnostic) || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("discarded field changed source validation", source, err)
		}
	}
}

func TestDeferredFoldObjectFieldsCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range []string{
		`labels:items:map(p=>{"used":p[1],"unused":[p[1]]}):foldl(0,(a,v)=>a+v.used:string:len)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1]]}):foldr(0,(v,a)=>[1]:foldl(a,(b,x)=>b+v.used:string:len))>5`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
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
				t.Fatal("partial field-projected query escaped cancellation", source, after, q, err)
			}
		}
	}
}
