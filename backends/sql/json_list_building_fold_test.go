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

func listBuildingFoldSources(itemRoleFixture) []string {
	const values = `labels:items:map(p=>p[1])`
	var sources []string
	for _, fold := range []string{
		`foldl([],(a,v)=>a:concat([v,v:type]))`,
		`foldr([],(v,a)=>[v,v:type]:concat(a))`,
		`foldl([],(a,v)=>[v,v:type]:concat(a))`,
		`foldr([],(v,a)=>a:concat([v,v:type]))`,
	} {
		for _, suffix := range []string{`:map(v=>v:type):json:exists`, `[0]:type:exists`, `[-1]:json:exists`} {
			sources = append(sources, values+":"+fold+suffix)
		}
	}
	return append(sources,
		values+`:foldl([],(a,v)=>a:concat([v,{"value":v},[v]])):map(v=>v:type):json:exists`,
		values+`:foldl([],(a,v)=>a:concat([v!])):exists`,
		values+`:foldr([],(v,a)=>[v!]:concat(a)):exists`,
		values+`:filter(v=>false):foldl([],(a,v)=>a:concat([v!])):size=0`,
		values+`:map(v=>v!):filter(v=>false):foldl([],(a,v)=>a:concat([v])):exists`,
		values+`:foldl([],(a,v)=>a:concat([[1]:map(a=>a+1),v])):size>0`,
		values+`:foldl([],(a,v)=>a:concat([[1]:map(x=>v:type),v])):size>0`,
		values+`:flatmap(v=>[v,null,0,{},[]]):foldl([],(a,v)=>[v,v:type]:concat(a)):map(v=>v:type):json:exists`,
		`other:items:any(p=>`+values+`:foldl([],(a,v)=>a:concat([{"key":p[0],"value":v}])):size>0)`,
		values+`:foldl([],(a,v)=>a:concat([v])):foldr([],(v,a)=>[v,v:type]:concat(a)):map(v=>v:type):json:exists`,
	)
}

func TestListBuildingFoldsBuild(t *testing.T) {
	for _, fixture := range deferredValueFixtures() {
		e := itemFieldFoldEngine(t, fixture)
		for _, source := range listBuildingFoldSources(fixture) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, fixture.metadata)...))
				if err != nil {
					t.Fatal(fixture.name, format, source, err)
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}

func TestListBuildingFoldRemovesRecursiveAccumulator(t *testing.T) {
	e := unusedFoldEngine(t)
	for _, source := range listBuildingFoldSources(itemRoleFixture{})[:12] {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), "(pos,acc,p)") || !strings.Contains(q.Where(), ".ord DESC,") && !strings.Contains(q.Where(), ".ord ASC,") {
			t.Fatal("literal chunks retained a recursive accumulator or lost ordering", source, q.Where())
		}
	}
}

func TestListBuildingFoldCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	p, err := e.Compile(context.Background(), listBuildingFoldSources(itemRoleFixture{})[0])
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
			t.Fatalf("checkpoint %d returned query %v, error %v", checkpoint, q, err)
		}
	}
}

func TestListBuildingFoldPreservesDiagnosticsAndExtensions(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.building.fold.touch", Name: "touch", Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for _, source := range []string{
		`labels:items:map(p=>[p[1],p[1]:type:touch]):foldl([],(a,v)=>a:concat([v])):exists`,
		`labels:items:map(p=>p[1]):foldl(["seed":touch],(a,v)=>a:concat([v])):exists`,
		`labels:items:map(p=>p[1]):foldl([],(a,v)=>a:concat([v,v:type:touch])):exists`,
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
		if err != nil || q == nil || calls != 2 || !strings.Contains(q.Where(), "(pos,acc,p)") {
			t.Fatal("custom path changed", source, calls, err)
		}
		requireConcatArgsUsed(t, q)
		t.Logf("custom calls=%d source=%s", calls, source)
	}
	for _, source := range []string{
		`labels:items:map(p=>[p[1],"\u0000"]):foldl([],(a,v)=>a:concat([v])):exists`,
		`labels:items:map(p=>p[1]):filter(v=>false):foldl([],(a,v)=>a:concat([v,"\u0000"])):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		var detail *hff.Error
		if q != nil || !errors.As(err, &detail) || detail.Code != hff.BackendError || detail.Span == nil || detail.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("original NUL diagnostic changed", source, err)
		}
	}
	source := `labels:items:map(p=>p[1]):filter(v=>false):foldl([],(a,v)=>a:concat([v,v:string:matches(".")])):exists`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	var detail *hff.Error
	if q != nil || !errors.As(err, &detail) || detail.Code != hff.BackendError || detail.Span == nil || detail.Span.Start != strings.Index(source, `"."`) {
		t.Fatal("empty source skipped original regex validation", err)
	}
}
