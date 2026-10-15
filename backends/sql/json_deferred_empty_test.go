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

func emptyDeferredSources() []string {
	left := `labels:items:flatmap(p=>[p[1]])`
	var sources []string
	for _, prefix := range []string{
		left + `:concat([])`, `[]:concat(` + left + `)`,
		left + `:concat([]:map(x=>"UNUSED"))`,
		`[]:map(x=>"UNUSED"):concat(` + left + `)`,
		left + `:concat(empty)`, `empty:concat(` + left + `)`,
	} {
		for _, suffix := range []string{
			`:map(v=>v:type):json:exists`, `:any(v=>v:type="bytes")`,
			`:all(v=>v:exists)`, `:count(v=>v:exists)>0`, `:size>0`,
			`:filter(v=>v:exists):map(v=>v:type):json:exists`,
			`:sum(v=>v:len)>0`, `:avg(v=>v:len)>0`,
			`:min(v=>v:len)>0`, `:max(v=>v:len)>0`,
			`:foldl(0,(a,v)=>a+1)>0`, `:foldr(0,(v,a)=>a+1)>0`,
			`:find(v=>v:exists):len>0`, `[0]:type="bytes"`,
			`:[list[bytes|null]]:json:exists`,
		} {
			sources = append(sources, prefix+suffix)
		}
	}
	for _, op := range []string{"=", "!=", "<", "<=", ">", ">="} {
		sources = append(sources, left+`:flatmap(v=>[]):concat([]):find(v=>true):len`+op+`(data:size+1)`)
	}
	sources = append(sources,
		`!(`+left+`:flatmap(v=>[]):concat([]):find(v=>true)!:len>0)`,
		left+`:flatmap(v=>[]):concat([]):find(v=>true)!:len>0`,
	)
	return append(sources,
		left+`:concat([]):foldl([],(a,v)=>a:concat([v])):map(v=>v:type):json:exists`,
		left+`:concat([]):foldr([],(v,a)=>[v]:concat(a)):map(v=>v:type):json:exists`,
		left+`:map(v=>v!):flatmap(v=>[]):concat([]):all(v=>false)`,
		left+`:flatmap(v=>[v!]:filter(x=>false)):concat([]):all(v=>false)`,
		left+`:flatmap(v=>[]):concat([]):sum=0`,
		left+`:flatmap(v=>[]):concat([]):avg=null`,
		left+`:flatmap(v=>[]):concat([]):find(v=>true):len=null`,
		left+`:flatmap(v=>[]):concat([])[0]:len=null`,
		left+`:flatmap(v=>[]):concat([]):find(v=>true):len>0`,
		left+`:flatmap(v=>[]):concat([])[0]:len>0`,
		left+`:concat([]:map(v=>gate!)):map(v=>v:type):json:exists`,
		left+`:concat([]):flatmap(v=>[v]):concat([]:map(v=>"UNUSED")):map(v=>v:type):json:exists`,
		left+`:concat([]):foldl([],(a,v)=>a:concat([]):concat([[v]])):flatmap(v=>v):map(v=>v:type):json:exists`,
		`[]:flatmap(v=>[v]):concat(empty):map(v=>"UNUSED"):json="[]"`,
		`[]:flatmap(v=>[v]):concat(empty):any(v=>v:string="UNUSED")`,
		`[]:flatmap(v=>[v]):concat(empty):all(v=>v:string="UNUSED")`,
		`[]:flatmap(v=>[v]):concat(empty):count(v=>v:string="UNUSED")=0`,
		`[]:flatmap(v=>[v]):concat(empty):sum(v=>1)=0`,
		`[]:flatmap(v=>[v]):concat(empty):avg(v=>1)=null`,
		`[]:flatmap(v=>[v]):concat(empty):min(v=>1)=null`,
		`[]:flatmap(v=>[v]):concat(empty):max(v=>1)=null`,
		`[]:flatmap(v=>[v]):concat(empty):foldl(7,(a,v)=>a+1)=7`,
		`[]:flatmap(v=>[v]):concat(empty):foldr(7,(v,a)=>a+1)=7`,
	)
}

func TestEmptyDeferredConcatOmitsValidationRows(t *testing.T) {
	e, err := engine.New(engine.WithSchema(emptyConcatSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range emptyDeferredSources() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(emptyConcatBindings(format, format == hffsql.JSONB)...))
			if err != nil {
				t.Fatal(source, format, err)
			}
			requireConcatArgsUsed(t, q)
			// Queries with a tagged source use the deferred path; the remaining
			// cases also exercise ordinary all-empty propagation for parity.
			for _, arg := range q.Args() {
				if arg == "UNUSED" && strings.Contains(source, "labels:items") {
					t.Error("unused empty callback parameter", source)
				}
			}
			// A recursive fold still has its own UNION ALL; zero-row validation
			// arms must not survive as physical input scans anywhere.
			if strings.Contains(q.Where(), "SELECT 1 AS n WHERE FALSE") || strings.Contains(q.Where(), "SELECT 1 WHERE FALSE") {
				t.Error("validation scope leaked into SQL", source)
			}
		}
	}
}

func TestEmptyDeferredConcatPreservesDiagnostics(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	left := `labels:items:flatmap(p=>[p[1]])`
	for _, source := range []string{
		`[]:map(x=>"\u0000FIRST"):concat(` + left + `):map(x=>"\u0000SECOND"):exists`,
		left + `:concat([]:map(x=>"\u0000FIRST")):any(x=>x:string="\u0000SECOND")`,
		left + `:concat([]):foldl(0,(a,v)=>"\u0000FIRST":len)+"\u0000SECOND":len>0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, `"\u0000FIRST"`) {
			t.Fatalf("expected earlier error for %s, got %v", source, err)
		}
	}
}

func TestEmptyDeferredConcatKeepsInvokeNullBuildValidation(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.empty.touch", Name: "touch", Receiver: schema.AnyType, Result: schema.StringType, NullPolicy: hff.InvokeNull, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	visited := 0
	implementation := hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
		visited++
		return c.Expression(schema.StringType, "'seen'::text")
	})
	p, err := e.Compile(context.Background(), `[]:map(x=>x:touch):concat(labels:items:flatmap(p=>[p[1]])):map(v=>v:type):json:exists`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithMethods(implementation), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if visited == 0 || strings.Contains(q.Where(), "'seen'") {
		t.Fatalf("empty callback must be validated without emitting its SQL: visits=%d query=%s", visited, q.Where())
	}
}

func TestEmptyDeferredConcatCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:flatmap(p=>[p[1]]):concat([]:map(x=>"UNUSED")):map(v=>v:[any]):concat([]):map(v=>v:type):json:exists`)
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("checkpoint%d returned partial%v err%v", after, q, err)
		}
	}
}

func TestEmptyDeferredNullComparisonValidatesRightSide(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.empty.number", Name: "emptytestnumber", Receiver: schema.StringType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(emptyConcatSchema()), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	left := `labels:items:flatmap(p=>[p[1]]):flatmap(v=>[]):find(v=>true):len`
	for _, rhs := range []string{`"\u0000":number`, `"x":matches(".")`} {
		p, err := e.Compile(context.Background(), left+`>(`+rhs+`)`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(emptyConcatBindings(hffsql.JSON, false)...))
		requireCode(t, err, hff.BackendError)
	}
	visited := 0
	implementation := hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
		visited++
		return c.Parameter(hff.Int(42))
	})
	p, err := e.Compile(context.Background(), left+`>"x":emptytestnumber`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithMethods(implementation), hffsql.WithBindings(emptyConcatBindings(hffsql.JSON, false)...))
	if err != nil {
		t.Fatal(err)
	}
	if visited == 0 {
		t.Fatal("RHS extension not validated")
	}
	for _, arg := range q.Args() {
		if arg == int64(42) || arg == "x" {
			t.Fatal("discarded RHS args leaked", q.Args())
		}
	}
	requireConcatArgsUsed(t, q)
}
