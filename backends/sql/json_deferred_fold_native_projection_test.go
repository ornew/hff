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

func nativeChildFoldSources(itemRoleFixture) []string {
	object := `labels:items:map(p=>{"used":p[1]:type:len,"unused":[p[1],p[1]]})`
	list := `labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}])`
	return []string{
		object + `:foldl(0,(a,v)=>a+v.used)>5`,
		object + `:foldr(0,(v,a)=>a+v["used"])>5`,
		object + `:foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v.used))>5`,
		object + `:foldl(0,(a,v)=>a+v.used+[1]:foldl(0,(b,v)=>b+v))>5`,
		object + `:foldl(true,(a,v)=>a v.used!:exists)`,
		`labels:items:map(p=>{"a":p[1]:type:len,"b":p[1]:type:len+1,"unused":[p[1]]}):foldl(true,(a,v)=>a v.b=v.a+1)`,
		`labels:items:map(p=>{"kind":p[1]:type,"present":p[1]:exists,"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.kind:len+v.present:string:len)>5`,
		`labels:items:map(p=>{"":p[1]:type,"日本.語":p[1]:type:len,"unused":[p[1]]}):foldl(0,(a,v)=>a+v[""]:len+v["日本.語"])>5`,
		`labels:items:map(p=>{"used":p[1]:type:duration,"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.used:type)):contains("null")`,
		`labels:items:map(p=>{"used":p[1]:type:len,"unused":p[1]!}):foldl(0,(a,v)=>a+v.used):exists`,
		`labels:items:map(p=>{"used":p[1]:type:len,"unused":null!}):foldl(0,(a,v)=>a+v.used):exists`,
		`labels:items:filter(p=>false):map(p=>{"used":p[1]:type:len,"unused":null!}):foldl(0,(a,v)=>a+v.used)=0`,
		`labels:items:flatmap(p=>[{"used":p[1]:type:len,"unused":p[1]},{"other":p[1]}]):foldl("",(a,v)=>a:concat(v["used"]:type)):len>5`,
		`labels:items:flatmap(p=>[{"used":p[1]:type:len},{"used":p[1]:type:len/2}]):foldl("",(a,v)=>a:concat(v.used:type)):len>5`,
		list + `:foldl(0,(a,v)=>a+v[0]:[int])>5`,
		list + `:foldr(0,(v,a)=>a+v[-2]:[int])>5`,
		list + `:foldl(0,(a,v)=>a+v[-0.0]:[int])>5`,
		list + `:foldl(0,(a,v)=>a+v[0]:type:len)>5`,
		list + `:foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v[0]:[int]))>5`,
		list + `:foldl(0,(a,v)=>a+v[0]:[int]+v[100]:exists:string:len)>5`,
		`labels:items:map(p=>[p[1]:type:len,p[1]:type:len/2]):foldl(0,(a,v)=>a+v[0])>5`,
		`labels:items:map(p=>[p[1]:type:len,p[1]:type:len/2]):foldr(0,(v,a)=>a+v[-2]+v[1])>5`,
		`labels:items:map(p=>[p[1]:type:len,p[1]:type:len+1,{"unused":p[1]}]):foldl(true,(a,v)=>a v[1]:[int]=v[0]:[int]+1)`,
		`labels:items:map(p=>[]:concat([p[1]:type:len,{"unused":p[1]}])):foldl(0,(a,v)=>a+v[0]:[int])>5`,
		`labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}]:concat([])):foldr(0,(v,a)=>a+v[-2]:[int])>5`,
		`labels:items:map(p=>[p[1]:type:len,null!]):foldl(0,(a,v)=>a+v[0]:[int]):exists`,
		object + `:foldl(null!,(a,v)=>v.used):exists`,
		object + `:foldl(0,(a,v)=>null!):exists`,
	}
}

func TestNativeChildDeferredFoldsBuild(t *testing.T) {
	for _, fixture := range deferredValueFixtures() {
		e := itemFieldFoldEngine(t, fixture)
		for _, source := range nativeChildFoldSources(fixture) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, fixture.metadata)...))
				if err != nil {
					t.Fatal(fixture.name, source, err)
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}

func TestNativeChildDeferredFoldsKeepRepresentationRejections(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, test := range []struct {
		source string
		start  int
	}{
		{`labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}]):foldl(0,(a,v)=>a+v[0])>0`, 70},
		{`labels:items:map(p=>["1ns":duration,"0000-01-01T00:00:00.000000001Z":timestamp,p[1]]):foldl(true,(a,v)=>a v[0]:nanoseconds=1 v[1]:year=0)`, 106},
	} {
		p, err := e.Compile(context.Background(), test.source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			var diagnostic *hff.Error
			if q != nil || !errors.As(err, &diagnostic) || diagnostic.Code != hff.BackendError || diagnostic.Message != "JSON cannot distinguish String, Bytes, Duration, and Timestamp at one structural position" || diagnostic.Span == nil || diagnostic.Span.Start != test.start {
				t.Fatal("original transport rejection changed", test.source, err)
			}
		}
	}
}

func TestNativeChildDeferredFoldCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	p, err := e.Compile(context.Background(), `labels:items:map(p=>{"a":p[1]:type,"b":p[1]:type:len,"unused":[p[1]]}):foldl(0,(a,v)=>a+v.a:len+v.b)>0`)
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for checkpoint := int64(1); checkpoint <= baseline.checks.Load(); checkpoint++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &buildCancellationContext{Context: base, after: checkpoint, cancel: cancel}
		q, err := hffsql.Build(ctx, p, options...)
		cancel()
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation escaped native projection", checkpoint, q, err)
		}
	}
}

func TestNativeChildDeferredFoldUsesDistinctColumns(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range []string{
		`labels:items:map(p=>{"a":p[1]:type:len,"b":p[1]:type:len+1,"unused":[p[1]]}):foldl(true,(a,v)=>a v.b=v.a+1)`,
		`labels:items:map(p=>[p[1]:type:len,p[1]:type:len+1,{"unused":p[1]}]):foldl(true,(a,v)=>a v[1]:[int]=v[0]:[int]+1)`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "SELECT arm,input_null,child_0_int_value,child_1_int_value,row_number()") {
			t.Fatal("selected children retained a packet or merged by kind", source, q.Where())
		}
		requireConcatArgsUsed(t, q)
	}
}

func TestNativeChildDeferredFoldPreservesDiagnosticsAndExtensions(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.native.child.touch", Name: "touch", Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for _, source := range []string{
		`labels:items:map(p=>{"used":p[1]:type:touch,"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.used)):exists`,
		`labels:items:map(p=>{"used":p[1]:type,"unused":[p[1]]}):foldl("":touch,(a,v)=>a:concat(v.used)):exists`,
		`labels:items:map(p=>{"used":p[1]:type,"unused":[p[1]]}):foldl("",(a,v)=>a:touch:concat(v.used)):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return c.Expression(schema.StringType, "{}", receiver)
		})))
		if err != nil || q == nil || calls != 1 || strings.Contains(q.Where(), "AS child_") {
			t.Fatal("custom translation path changed", source, calls, err)
		}
		t.Logf("custom calls=%d source=%s", calls, source)
		requireConcatArgsUsed(t, q)
	}
	for _, source := range []string{
		`labels:items:map(p=>{"used":p[1]:type:len,"unused":"\u0000"}):foldl(0,(a,v)=>a+v.used)>0`,
		`labels:items:map(p=>[p[1]:type:len,"\u0000"]):foldl(0,(a,v)=>a+v[0]:[int])>0`,
		`labels:items:map(p=>{"used":p[1]:type:len,"unused":[p[1]]}):foldl("\u0000",(a,v)=>a:concat(v.used:string)):exists`,
		`labels:items:map(p=>{"used":p[1]:type:len,"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.used:string):concat("\u0000")):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		var diagnostic *hff.Error
		if q != nil || !errors.As(err, &diagnostic) || diagnostic.Code != hff.BackendError || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("original diagnostic changed", source, err)
		}
	}
}
