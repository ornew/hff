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

func nativeNumericFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	numbers := values + `:flatmap(v=>[v:type:len,v:type:len/2])`
	return []string{
		numbers + `:foldl(0.0,(a,n)=>a+n)>0`,
		numbers + `:foldr(0.0,(n,a)=>a-n)<0`,
		numbers + `:foldl("",(a,n)=>a:concat((n:[int]:exists):string)):contains("true","false")`,
		numbers + `:foldl([],(a,n)=>a:concat([n:type])):size>0`,
		numbers + `:foldl([],(a,n)=>a:concat([n])):json:exists`,
		numbers + `:foldl(0.0,(a,n)=>a+n!)>0`,
		numbers + `:foldl(0.0,(a,n)=>[1]:foldl(a,(b,x)=>b+n))>0`,
		numbers + `:foldr(0.0,(n,a)=>[1]:foldr(a,(x,b)=>b-n))<0`,
		numbers + `:foldl(0.0,(a,n)=>a+n+[1]:foldl(0,(b,n)=>b+n))>0`,
		numbers + `:foldl(0.0,(a,n)=>a+n+other:items:map(p=>p[0]:len):sum)>0`,
		values + `:filter(v=>false):flatmap(v=>[1,2.0]):foldl(7.0,(a,n)=>a+n)=7.0`,
		values + `:map(v=>v!):filter(v=>false):flatmap(v=>[1,2.0]):foldl(0.0,(a,n)=>a+n):exists`,
		values + `:flatmap(v=>[]:concat([1,2.0])):foldl(0.0,(a,n)=>a+n)>0`,
		values + `:flatmap(v=>[1,2.0]:concat([])):foldr(0.0,(n,a)=>a+n)>0`,
		values + `:flatmap(v=>[9007199254740993,9007199254740992.0]):foldl(true,(a,n)=>a (n:[float]:exists | n=9007199254740993))`,
		values + `:flatmap(v=>[-9223372036854775808,9223372036854775807,0.0]):foldl("",(a,n)=>a:concat((n:[int]:exists):string))="truetruefalsetruetruefalse"`,
		values + `:flatmap(v=>[1,-0.0,5e-324]):foldl("",(a,n)=>a:concat((n:[float]:exists):string)):contains("true","false")`,
		values + `:flatmap(v=>[1,1.0/0]):foldl(true,(a,n)=>a (n=null | n=1))`,
		values + `:flatmap(v=>[1,2.0]):map(n=>n!):foldl(null!,(a,n)=>n):exists`,
		values + `:flatmap(v=>[1,2.0]):foldl(0.0,(a,n)=>null!)=null`,
		// Homogeneous native inputs retain the established ordinary path.
		values + `:flatmap(v=>[v:type:len,v:type:len+1]):foldl(0,(a,n)=>a+n)>0`,
	}
}

func TestNativeNumericDeferredFoldsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e := itemFieldFoldEngine(t, tc)
		for _, source := range nativeNumericFoldSources(tc) {
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

func TestNativeNumericFoldUsesSeparateColumns(t *testing.T) {
	e := unusedFoldEngine(t)
	source := `labels:items:map(p=>p[1]):flatmap(v=>[v:type:len,v:type:len/2]):foldl(0.0,(a,n)=>a+n)>0`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"SELECT arm,int_value,float_value,row_number()", "NULL::double precision AS float_value", "NULL::bigint AS int_value", ".int_value AS value", ".float_value AS value", "WITH RECURSIVE"} {
		if !strings.Contains(q.Where(), required) {
			t.Fatal("numeric input transport missing", required)
		}
	}
	requireConcatArgsUsed(t, q)
}

func TestNativeNumericFoldRetainsDiagnosticsAndCustomCalls(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.numeric.touch", Name: "touch", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for i, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(v=>[1:touch,2.0]):foldl(0.0,(a,n)=>a+n)>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>[1,2.0]):foldl(0:touch,(a,n)=>a+n)>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>[1,2.0]):foldl(0,(a,n)=>a:touch+n)>0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		calls := 0
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, r hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return c.Expression(schema.IntType, "{}", r)
		})))
		// Source and seed translate once; the callback translates once for
		// each of the two input descriptors on the original packet path.
		if err != nil || calls != []int{1, 1, 2}[i] || strings.Contains(q.Where(), "SELECT arm,int_value,float_value") {
			t.Fatal("custom translation path changed", source, calls, err)
		}
		requireConcatArgsUsed(t, q)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(v=>[1,2.0]):foldl("\u0000",(a,n)=>a:concat(n:type)):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[1,2.0]):foldl("",(a,n)=>a:concat(n:type):concat("\u0000")):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		requireCode(t, err, hff.BackendError)
		var diagnostic *hff.Error
		if q != nil || !errors.As(err, &diagnostic) || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("original diagnostic changed", source, err)
		}
	}
}

func TestNativeNumericFoldCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):flatmap(v=>[v:type:len,v:type:len/2]):foldl(0.0,(a,n)=>a+n)>0`)
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
			t.Fatal("partial numeric transport escaped cancellation", after, q, err)
		}
	}
}
