package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/schema"
)

const emptySelectionFixedFold = `labels:items:map(p=>[[p[1]]:filter(x=>false)[0],p[1]]):foldl("",(a,v)=>a:concat(v[0]:type)):len>5`

func fixedFoldListSources(itemRoleFixture) []string {
	input := `labels:items:map(p=>[p[1],{"unused":[p[1],p[1]]}])`
	sources := []string{
		input + `:foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		input + `:foldr(0,(v,a)=>a+v[-2]:string:len)>5`,
		input + `:foldl(0,(a,v)=>a+v[0.0]:string:len)>5`,
		input + `:foldl(0,(a,v)=>a+v[-0.0]:string:len)>5`,
		input + `:foldl("",(a,v)=>a:concat(v[0]:type)):len>10`,
		input + `:foldl(0,(a,v)=>a+v[0]!:string:len)>5`,
		input + `:foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v[0]:string:len))>5`,
		input + `:foldl(0,(a,v)=>a+v[0]:string:len+[1]:foldl(0,(b,v)=>b+v))>5`,
		input + `:foldl(true,(a,v)=>a v[100]=null)`,
		input + `:foldl(true,(a,v)=>a v[-100]=null)`,
		input + `:foldl(true,(a,v)=>a v[9223372036854775807]=null)`,
		`labels:items:map(p=>[p[1],"middle",p[1]]):foldl(0,(a,v)=>a+v[0]:string:len+v[-1]:string:len)>10`,
		`labels:items:map(p=>[[],p[1]]):foldl(0,(a,v)=>a+v[1.0]:string:len)>5`,
		`labels:items:map(p=>[[],p[1]]):foldl(0,(a,v)=>a+v[0]:size)=0`,
		`labels:items:map(p=>[p[1]]:concat([])):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		`labels:items:map(p=>[]:concat([p[1]])):foldl(0,(a,v)=>a+v[-1]:string:len)>5`,
		`labels:items:map(p=>[[p[1],p[1]],{"unused":p[1]}]):foldl(0,(a,v)=>a+v[0]:size)>2`,
		`labels:items:map(p=>[{"used":p[1]},[p[1]]]):foldl("",(a,v)=>a:concat(v[0].used:type)):len>10`,
		`labels:items:map(p=>[[p[1]]:filter(x=>x!=null p[0]:len>0)[0],{"unused":[p[1]]}]):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		emptySelectionFixedFold,
		`labels:items:map(p=>[p[1],null!]):foldl(0,(a,v)=>a+v[0]:string:len):exists`,
		`labels:items:filter(p=>false):map(p=>[p[1],null!]):foldl(0,(a,v)=>a+v[0]:string:len)=0`,
		input + `:foldl(0,(a,v)=>a+v:size)>0`,
		input + `:foldl(0,(a,v)=>a+v![0]:string:len)>5`,
		input + `:foldl(0,(a,v)=>a+v[v:size-2]:string:len)>5`,
		input + `:foldl(true,(a,v)=>a v[0.5]=null)`,
	}
	return sources
}

func TestFixedDeferredFoldListsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e := itemFieldFoldEngine(t, tc)
		for _, source := range fixedFoldListSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if fixedFoldRejectsOriginalTransport(tc, source) {
					requireCode(t, err, hff.BackendError)
					var diagnostic *hff.Error
					if q != nil || !errors.As(err, &diagnostic) || diagnostic.Message != "JSON cannot distinguish String, Bytes, Duration, and Timestamp at one structural position" || diagnostic.Span == nil || diagnostic.Span.Start != 21 {
						t.Fatal("original transport rejection changed", tc.name, format, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(tc.name, source, format, err)
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}

func TestFixedDeferredFoldListKeepsSourceDiagnostics(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range []string{
		`labels:items:map(p=>[p[1],"\u0000"]):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		`labels:items:map(p=>[p[1]]:concat(["\u0000"])):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		`labels:items:map(p=>[p[1],1]):foldl("\u0000",(a,v)=>a:concat(v[0]:string))="body"`,
		`labels:items:map(p=>[p[1],1]):foldl("seed",(a,v)=>a:concat(v[0]:string):concat("\u0000"))="body"`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		requireCode(t, err, hff.BackendError)
		var diagnostic *hff.Error
		if q != nil || !errors.As(err, &diagnostic) || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("source diagnostic changed", source, err)
		}
	}
}

func TestFixedDeferredFoldListKeepsCustomTranslation(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.fixed.touch", Name: "touch", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for _, source := range []string{
		`labels:items:map(p=>[p[1],1:touch]):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		`labels:items:map(p=>[p[1],1]):foldl(0:touch,(a,v)=>a+v[0]:string:len)>5`,
		`labels:items:map(p=>[p[1],1]):foldl(0,(a,v)=>(a+v[0]:string:len):touch)>5`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, r hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return c.Expression(schema.IntType, "{}", r)
		})))
		if err != nil || calls != 1 {
			t.Fatal(source, calls, err)
		}
		requireConcatArgsUsed(t, q)
	}
}

// Empty filtered children still validate the original tagged-string transport;
// known ambiguity is a Build error rather than permission to skip validation.
func fixedFoldRejectsOriginalTransport(tc itemRoleFixture, source string) bool {
	return source == emptySelectionFixedFold && tc.mixed && !tc.metadata && (tc.tag == "bytes" || tc.tag == "duration" || tc.tag == "timestamp")
}

func buildableFixedFoldListSources(tc itemRoleFixture) []string {
	var sources []string
	for _, source := range fixedFoldListSources(tc) {
		if !fixedFoldRejectsOriginalTransport(tc, source) {
			sources = append(sources, source)
		}
	}
	return sources
}

func TestFixedFoldListRemovesRowTransport(t *testing.T) {
	e := unusedFoldEngine(t)
	source := `labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v[0]:string:len)>5`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	// The source items() still aggregates its rows. The fixed constructor
	// must not add another row aggregation for the fold transport.
	if !strings.Contains(q.Where(), "WITH RECURSIVE") || strings.Count(q.Where(), "json_agg(") != 1 {
		t.Fatal("fixed list retained generic row aggregation or lost fold recursion")
	}
	requireConcatArgsUsed(t, q)
}

func TestFixedFoldListCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range []string{
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v[-1]:string:len)>5`,
		`labels:items:map(p=>[[p[1]]:filter(x=>x!=null p[0]:len>0)[0],p[1]]):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
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
				t.Fatal("partial fixed-list query escaped cancellation", source, after, q, err)
			}
		}
	}
}
