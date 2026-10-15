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

func itemFlatmapPipelineSources(tc itemRoleFixture) []string {
	left := `labels:items:map(p=>p[1])`
	right := `other:items:map(p=>p[1])`
	sources := []string{
		left + `:flatmap(v=>[v]):map(v=>v:type):json=` + left + `:map(v=>v:type):json`,
		left + `:flatmap(v=>[v,v]):map(v=>v:type):json=` + left + `:map(v=>v:type):flatmap(v=>[v,v]):json`,
		left + `:flatmap(v=>[]):any(v=>true)`,
		left + `:flatmap(v=>[]):map(v=>"unused"):filter(v=>true):all(v=>false)`,
		left + `:flatmap(v=>[]):flatmap(v=>[v]):map(v=>v:type):size=0`,
		left + `:flatmap(v=>[]):flatmap(v=>v):map(v=>v:type):size=0`,
		left + `:flatmap(v=>[v!]):filter(v=>false):any(v=>true)=null`,
		left + `:filter(v=>false):flatmap(v=>[v!]):all(v=>true)`,
		left + `:flatmap(v=>[v]):map(v=>v:[any]):concat(` + right + `:flatmap(v=>[v])):map(v=>v:type):json=` + left + `:map(v=>v:type):concat(` + right + `:map(v=>v:type)):json`,
		left + `:flatmap(v=>[v]:concat([v])):map(v=>v:type):count(v=>v="` + tc.tag + `")>0`,
		left + `:flatmap(v=>[v]:filter(x=>x:exists)):map(v=>v:type):contains("null")`,
		left + `:flatmap(v=>[v]:map(x=>x:[any])):map(v=>v:type):json=` + left + `:map(v=>v:type):json`,
		left + `:flatmap(v=>[v]:flatmap(x=>[x,x])):map(v=>v:type):count(v=>v="` + tc.tag + `")>0`,
		left + `:flatmap(v=>other:items:map(p=>p[1])):map(v=>v:type):size=labels:size*other:size`,
		left + `:flatmap(v=>other:items:map(p=>v)):map(v=>v:type):json=` + left + `:flatmap(v=>other:items:map(p=>v:type)):json`,
		left + `:flatmap(v=>[v]):any(v=>v)`,
		left + `:flatmap(v=>[v]):filter(v=>v):all(v=>v)`,
		left + `:flatmap(v=>[v]):count(v=>v:exists)>0`,
		left + `:flatmap(v=>[v,[v]:size]):map(v=>v:type):contains("number")`,
		`labels:items:flatmap(p=>[p[1]]):map(v=>v:type):json=` + left + `:map(v=>v:type):json`,
	}
	if tc.name == "nested/map" || tc.name == "list-bytes/any-field" {
		sources = append(sources,
			left+`:flatmap(v=>v):map(v=>[v:type,v:json]):json:exists`,
			left+`:flatmap(v=>v):filter(v=>v):map(v=>v:type):contains("bytes")`,
		)
	}
	return sources
}

func flatmapPipelineFixtures() []itemRoleFixture {
	fixture := itemRoleFixture{name: "list-bytes/any-field", tag: "list", mixed: true, metadata: true,
		typ:   schema.NullableType(schema.Struct(schema.Optional(schema.Field("app", schema.AnyType)), schema.Optional(schema.Field("z", schema.NullableType(schema.ListOf(schema.BytesType)))))),
		first: hff.List(hff.Bytes([]byte("a"))), second: hff.List(hff.Bytes([]byte("b"))), zero: hff.List()}
	for _, native := range []hff.Value{hff.List(), hff.List(hff.String("YQ==")), hff.List(hff.Int(1), hff.Float(1)), hff.Null()} {
		object := hff.Object(map[string]hff.Value{"app": native, "z": fixture.first})
		fixture.extraPairs = append(fixture.extraPairs, [2]hff.Value{object, object})
	}
	return append(itemMapPipelineFixtures(), fixture)
}

func TestJSONFlatmapPipelinesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range flatmapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemFlatmapPipelineSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestJSONFlatmapPipelineRetainsFloatLoss(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.ListOf(schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source     string
		serializes bool
	}{
		{`labels:items:map(p=>p[1]):flatmap(v=>v):map(v=>v:json):exists`, true},
		{`labels:items:map(p=>p[1]):flatmap(v=>[v]):map(v=>v[0]:json):exists`, true},
		{`labels:items:map(p=>p[1]):flatmap(v=>v):json:exists`, true},
		{`labels:items:map(p=>p[1]):flatmap(v=>v):map(v=>v:type):exists`, false},
		{`labels:items:map(p=>p[1]):flatmap(v=>[v,v]):map(v=>v[0]:json):exists`, true},
		{`labels:items:map(p=>p[1]):flatmap(v=>[v]:concat([v])):map(v=>v[0]:json):exists`, true},
		{`labels:items:map(p=>p[1]):flatmap(v=>[v,v]):map(v=>v:type):exists`, false},
		{`labels:items:flatmap(p=>[p[1],p[1]]):map(v=>v[0]:json):exists`, true},
		{`labels:items:flatmap(p=>[[p[1]],[p[1]]]):flatmap(v=>v):map(v=>v[0]:json):exists`, true},
		{`labels:items:flatmap(p=>[p[1],p[1]]):map(v=>v:type):exists`, false},
	} {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if (err != nil) != (format == hffsql.JSONB && tc.serializes) {
				t.Errorf("%s %s: %v", format, tc.source, err)
			}
		}
	}
}

func flatmapFailureFixture() itemRoleFixture {
	parts := func(key hff.Value, values hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"key": key, "parts": values})
	}
	good := parts(hff.String("parts"), hff.List(hff.Int(1), hff.String("a")))
	bad := parts(hff.Null(), hff.List(hff.Int(2)))
	empty := parts(hff.String("parts"), hff.List())
	pair := func(a, z hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"app": a, "z": z}) }
	fixture := itemRoleFixture{name: "failure/map", tag: "object", typ: schema.NullableType(schema.MapOf(schema.AnyType)), first: good, second: bad, zero: hff.Null(), metadata: true}
	for _, values := range [][2]hff.Value{
		{hff.Null(), bad}, {bad, hff.Null()}, {empty, bad}, {bad, empty},
		{parts(hff.String("parts"), hff.Null()), bad}, {bad, parts(hff.String("parts"), hff.Null())},
		{hff.List(hff.Int(1), hff.Null()), hff.List(hff.String("a"))},
		{hff.List(), hff.List(hff.Float(1), hff.Float(0))},
	} {
		object := pair(values[0], values[1])
		fixture.extraPairs = append(fixture.extraPairs, [2]hff.Value{object, object})
	}
	return fixture
}

func flatmapFailureSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	get := values + `:flatmap(v=>v:get(v.key!))`
	return []string{
		get + `:map(x=>x:type)=null`,
		get + `:map(x=>x:type):exists`,
		get + `:filter(x=>false):all(x=>true)=null`,
		get + `:concat([]):map(x=>x:type):json="null"`,
		get + `:flatmap(x=>[x!]):map(x=>x:type)=null`,
		values + `:flatmap(v=>v):map(x=>x:type):json:exists`,
		values + `:flatmap(v=>v):any(x=>x)`,
	}
}

func TestJSONFlatmapFailurePipelinesBuild(t *testing.T) {
	ctx := context.Background()
	tc := flatmapFailureFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range flatmapFailureSources(tc) {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, true)...)); err != nil {
				t.Fatal(source, format, err)
			}
		}
	}
}

func TestFlatmapPipelinePreparesInputsBeforeConsumer(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []bool{false, true} {
		early, late := `"SOURCE"`, `"TERMINAL"`
		if invalid {
			early, late = `"\u0000SOURCE"`, `"\u0000TERMINAL"`
		}
		for _, list := range []string{`[v,` + early + `]`, `[v]:flatmap(x=>[x,` + early + `])`} {
			source := `labels:items:map(p=>p[1]):flatmap(v=>` + list + `):map(v=>` + late + `):exists`
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
			if invalid {
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, early) {
					t.Fatalf("expected source-stage diagnostic for %s, got %v", source, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			earlyIndex, lateIndex := -1, -1
			for i, arg := range q.Args() {
				if arg == "SOURCE" && earlyIndex == -1 {
					earlyIndex = i
				}
				if arg == "TERMINAL" && lateIndex == -1 {
					lateIndex = i
				}
			}
			if earlyIndex < 0 || lateIndex < 0 || earlyIndex >= lateIndex {
				t.Fatalf("source arguments must precede terminal arguments: %v", q.Args())
			}
		}
	}
}

func TestFlatmapEmptyPipelineValidatesCallbacks(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{
		`:map(v=>"\u0000"):exists`,
		`:filter(v=>"\u0000"="x"):exists`,
		`:map(v=>"\u0000"):flatmap(v=>[v]):exists`,
		`:flatmap(v=>["\u0000"]):any(v=>true)`,
		`:concat([]):map(v=>"\u0000"):exists`,
		`:any(v=>"\u0000"="x")`,
		`:flatmap(v=>null):map(v=>"\u0000"):exists`,
		`:flatmap(v=>v):map(v=>"\u0000"):exists`,
	} {
		source := `labels:items:map(p=>p[1]):flatmap(v=>[])` + suffix
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Errorf("expected callback diagnostic for %s, got %v", source, err)
		}
	}
	_, err = e.Compile(ctx, `labels:items:map(p=>p[1]):flatmap(v=>[]):flatmap(v=>1):map(v=>"\u0000"):exists`)
	requireCode(t, err, hff.TypeError)
	for _, suffix := range []string{`:map(v=>"a"~"(?i)A"):exists`, `:filter(v=>"a"~"(?i)A"):exists`} {
		p, err := e.Compile(ctx, `labels:items:map(p=>p[1]):flatmap(v=>[])`+suffix)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		requireCode(t, err, hff.BackendError)
	}
}

func TestFlatmapEmptyArmDoesNotAddValueFamilies(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	source := `labels:items:map(p=>p[1]):flatmap(v=>[]):map(v=>"unused"):concat(labels:items:map(p=>p[1])):map(v=>v):exists`
	p, err := e.Compile(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))); err != nil {
		t.Fatal(err)
	}
}

func TestFlatmapPipelinePreservesCancellationAtEveryCheckpoint(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(v=>[v,v]):concat(labels:items:map(p=>p[1]):flatmap(v=>[v])):map(v=>v:type):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v]:concat([v,v])):map(v=>v:type):exists`,
		`labels:items:flatmap(p=>[p[1]!,p[1]!]):map(v=>v:type):exists`,
		`labels:items:flatmap(p=>[[p[1]],[p[1]]]):flatmap(v=>v):map(v=>v:type):exists`,
		`labels:items:flatmap(p=>[p[1],p[0],p[1]]):map(v=>v:type):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v]:flatmap(x=>[x,v])):map(v=>v:type):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[]):flatmap(v=>v):map(v=>v:type):exists`,
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
				t.Fatalf("%s checkpoint%d partialquery%v error%v", source, after, q, err)
			}
		}
	}
}
