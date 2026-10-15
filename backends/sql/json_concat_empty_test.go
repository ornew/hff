package sql_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestEmptyConcatDoesNotUnionEmptyArrays(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("data", schema.IntType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`data:concat([]):sum>0`, `[]:concat(data):sum>0`,
		`data:concat([]):json:exists`, `data:concat([]):any(v=>v>0)`,
		`data:concat([]:map(x=>x:string)):size>0`,
		`data:concat(data:filter(x=>false)):size>0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)))
			if err != nil {
				t.Fatal(format, source, err)
			}
			if strings.Contains(q.Where(), "UNION ALL") {
				t.Errorf("%s %s still splices an empty concat arm", format, source)
			}
			requireConcatArgsUsed(t, q)
		}
	}
}

func TestEmptyConcatValidatesDiscardedCallbacksInOrder(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`[]:map(x=>"\u0000FIRST"):concat(labels:items:map(p=>"\u0000SECOND")):map(v=>v:type):exists`,
		`labels:items:map(p=>p[1]):concat([]:map(x=>"\u0000FIRST")):map(v=>"\u0000SECOND"):exists`,
		`labels:items:map(p=>p[1]):concat([]):map(v=>"\u0000FIRST"):concat([]:map(x=>"\u0000SECOND")):map(v=>v:type):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, `"\u0000FIRST"`) {
			t.Fatalf("expected the earlier callback diagnostic for %s, got %v", source, err)
		}
	}
	source := `[]:map(x=>x:string:matches(".")):concat(labels:items:map(p=>p[1])):map(v=>v:type):exists`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	requireCode(t, err, hff.BackendError)
}

func TestEmptyConcatKeepsInvokeNullBuildValidation(t *testing.T) {
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
	p, err := e.Compile(context.Background(), `[]:map(x=>x:touch):concat(labels:items:map(p=>p[1])):map(v=>v:type):json:exists`)
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

func requireConcatArgsUsed(t *testing.T, q *hffsql.Query) {
	t.Helper()
	used := map[int]bool{}
	for _, match := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(q.Where(), -1) {
		index, _ := strconv.Atoi(match[1])
		used[index] = true
	}
	for i := range q.Args() {
		if !used[i+1] {
			t.Errorf("argument %d has no SQL reference: %v", i+1, q.Args())
		}
	}
}

func emptyConcatSchema() hff.Type {
	return schema.Struct(
		schema.Optional(schema.Nullable(schema.List("data", schema.NullableType(schema.Union(schema.IntType, schema.FloatType))))),
		schema.Optional(schema.Nullable(schema.List("empty", schema.NeverType))),
		schema.Optional(schema.Nullable(schema.Map("labels", schema.NullableType(schema.BytesType)))),
		schema.Optional(schema.Nullable(schema.Map("zero", schema.NeverType))),
		schema.Nullable(schema.Bool("gate")),
	)
}

func emptyConcatBindings(format hffsql.JSONFormat, metadata bool) []hffsql.Binding {
	var options []hffsql.JSONBindingOption
	if metadata {
		options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	}
	return []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...),
		hffsql.BindJSON(hff.Path{"empty"}, hffsql.Column("records", "empty"), format),
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format),
		hffsql.BindJSON(hff.Path{"zero"}, hffsql.Column("records", "zero"), format),
		hffsql.Bind(hff.Path{"gate"}, hffsql.Column("records", "gate")),
	}
}

func emptyConcatSources() []string {
	ordinary := []string{
		`data:concat([]):sum>0`, `[]:concat(data):sum>0`,
		`data:concat(empty):size=0`, `empty:concat(data):size=0`,
		`data:concat(empty!)=null`, `empty!:concat(data)=null`,
		`data!:concat([]):exists`, `[]:concat(data!):exists`,
		`data:concat([]):map(x=>x:type):json=data:map(x=>x:type):json`,
		`[]:map(x=>"UNUSED"):concat(data):map(x=>x:type):json=data:map(x=>x:type):json`,
		`data:map(x=>x!):filter(x=>false):concat([]):exists`,
		`data:map(x=>x!):filter(x=>false):concat(null)=null`,
		`data:concat(data:filter(x=>false)):size=data:size`,
		`data:concat(empty):json=data:json`,
		`data:concat([]):map(x=>x:[float]):exists`,
		`data:concat([]):concat(empty):all(x=>x:exists)`,
	}
	left := `labels:items:map(p=>p[1])`
	for _, prefix := range []string{left + `:concat([])`, `[]:concat(` + left + `)`, left + `:concat(empty)`, left + `:concat(empty!)`} {
		ordinary = append(ordinary,
			prefix+`:map(v=>v:type):json:exists`, prefix+`:any(v=>v:type="bytes")`,
			prefix+`:all(v=>v:exists)`, prefix+`:count(v=>v:exists)>0`,
		)
	}
	return append(ordinary,
		left+`:concat([]:map(x=>"UNUSED")):map(v=>v:type):json=`+left+`:map(v=>v:type):json`,
		left+`:concat([]):map(v=>v:[any]):concat([]):map(v=>v:type):json=`+left+`:map(v=>v:type):json`,
		`labels:items:map(p=>p[1]!):concat([]):map(v=>v:type):concat(null):type="null"`,
		`labels:items:map(p=>p[1]!):filter(v=>false):concat([]):all(v=>true)`,
		`labels:items:filter(p=>false):map(p=>p[1]!):concat([]):all(v=>true)`,
		`zero:items:map(p=>p[1]):concat([]):map(v=>"UNUSED"):json="[]"`,
		`zero:items:map(p=>p[1]):concat([]):any(v=>v:string="UNUSED")`,
		`zero:items:map(p=>p[1]):concat([]):all(v=>v:string="UNUSED")`,
		`zero:items:map(p=>p[1]):concat([]):count(v=>v:string="UNUSED")=0`,
		`zero!:items:map(p=>p[1]):concat([]):map(v=>v:type):type="null"`,
		left+`:concat([gate!]:filter(v=>false)):any(v=>v:exists)`,
	)
}

func TestEmptyConcatPipelineForwardsSingleStream(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):concat([]):map(v=>v:type):json:exists`,
		`[]:concat(labels:items:map(p=>p[1])):any(v=>v:type="bytes")`,
		`labels:items:map(p=>p[1]):concat([]:map(x=>"UNUSED")):map(v=>v:type):json:exists`,
		`labels:items:map(p=>p[1]):concat([]):map(v=>v:[any]):concat([]):map(v=>v:type):json:exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), "UNION ALL") {
			t.Errorf("%s still unions a known-empty stream", source)
		}
		requireConcatArgsUsed(t, q)
		for _, arg := range q.Args() {
			if arg == "UNUSED" {
				t.Error("empty callback argument leaked into the actual query", source)
			}
		}
	}
}

func TestEmptyConcatPipelineAllEmpty(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Map("zero", schema.NeverType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`zero:items:map(p=>p[1]):concat([]):map(v=>"UNUSED"):json="[]"`,
		`zero:items:map(p=>p[1]):concat([]):any(v=>v:string="UNUSED")`,
		`zero:items:map(p=>p[1]):concat([]):all(v=>v:string="UNUSED")`,
		`zero:items:map(p=>p[1]):concat([]):count(v=>v:string="UNUSED")=0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"zero"}, hffsql.Column("records", "zero"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), "UNION ALL") {
			t.Error("all-empty concat still joins row streams", source)
		}
		for _, arg := range q.Args() {
			if arg == "UNUSED" {
				t.Error("all-empty callback argument leaked into SQL", source)
			}
		}
		requireConcatArgsUsed(t, q)
	}
}

func TestEmptyConcatCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):concat([]:map(x=>"UNUSED")):map(v=>v:[any]):concat([]):map(v=>v:type):json:exists`)
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
