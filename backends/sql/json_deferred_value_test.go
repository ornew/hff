package sql_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func deferredValueSources(tc itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	types := values + `:map(v=>v:type):json`
	return []string{
		values + `:map(v=>[v]):flatmap(xs=>xs):map(v=>v:type):json=` + types,
		values + `:map(v=>[[v]]):flatmap(xs=>xs):flatmap(xs=>xs):map(v=>v:type):json=` + types,
		values + `:map(v=>{"payload":v}):map(o=>o.payload:type):json=` + types,
		values + `:map(v=>{"payload":[v]}):flatmap(o=>o.payload):map(v=>v:type):json=` + types,
		values + `:map(v=>[v,v]):map(xs=>xs[-1]:type):json=` + types,
		values + `:map(v=>[v,v]):map(xs=>xs[labels:size%3-1]:type):contains("` + tc.tag + `")`,
		values + `:map(v=>{"payload":v}):map(o=>o:get("payload"):type):json=` + types,
		values + `:map(v=>{"app":v,"z":v}):map(o=>o:get(labels:keys[0]):type):json=` + types,
		values + `:map(v=>[v]):map(xs=>xs:[list[any]]):flatmap(xs=>xs):map(v=>v:type):json=` + types,
		values + `:map(v=>{"payload":v}):map(o=>o:[map[any]].payload:type):json=` + types,
		values + `:map(v=>{"payload":[v!]}):flatmap(o=>o.payload):filter(v=>false):all(v=>true)`,
		values + `:map(v=>[v]):map(xs=>xs[0]:json):json=` + values + `:map(v=>v:json):json`,
		values + `:map(v=>{"payload":[v]}):map(o=>o:json):exists`,
		values + `:map(v=>[v,null]):map(xs=>xs:size):all(n=>n=2)`,
		values + `:map(v=>{"a":v,"b":null}):map(o=>o:size):all(n=>n=2)`,
		values + `:map(v=>[]):map(xs=>[xs]):flatmap(xs=>xs):all(xs=>xs:type="list")`,
		values + `:map(v=>[v,v]):filter(xs=>xs):flatmap(xs=>xs):map(v=>v:type):contains("` + tc.tag + `")`,
		values + `:map(v=>{"payload":v}):filter(o=>o):map(o=>o.payload:type):json=` + types,
		values + `:map(v=>[[v]][0][0]):map(v=>v:type):json=` + types,
		values + `:map(v=>[{"x":v}][labels:size%1]:get(null!)):exists`,
		values + `:map(v=>{"kind":v:type}):json:exists`,
		values + `:map(v=>{"payload":v}):map(o=>[o.payload]):flatmap(xs=>xs):map(v=>v:type):json=` + types,
		`labels:items:map(p=>[p[0],p[1]]):json=items_json`,
		values + `:map(v=>[v]):flatmap(xs=>other:items:map(p=>xs[0])):map(v=>v:type):json=` + values + `:flatmap(v=>other:items:map(p=>v:type)):json`,
		values + `:map(v=>[v]):flatmap(v=>other:items:map(p=>v[0])):map(v=>v:type):json=` + values + `:flatmap(v=>other:items:map(p=>v:type)):json`,
	}
}

func deferredValueFixtures() []itemRoleFixture {
	return append(flatmapPipelineFixtures(), itemRoleFixture{name: "string-lists/map", tag: "list",
		typ:   schema.NullableType(schema.MapOf(schema.NullableType(schema.ListOf(schema.StringType)))),
		first: hff.List(hff.String("a")), second: hff.List(hff.String("b")), zero: hff.List()})
}

func TestDeferredCollectionValuesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredValueSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Error(tc.name, format, source, err)
					continue
				}
				used := map[int]bool{}
				for _, match := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(q.Where(), -1) {
					index, _ := strconv.Atoi(match[1])
					used[index] = true
				}
				for i := range q.Args() {
					if !used[i+1] {
						t.Errorf("%s %s argument%d is unused: %s", tc.name, format, i+1, source)
					}
				}
			}
		}
	}
}

func TestDeferredNativeLeafGuardRetainsFloatProvenance(t *testing.T) {
	ctx := context.Background()
	for _, element := range []hff.Type{schema.StringType, schema.FloatType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType), schema.List("groups", schema.ListOf(element)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		typeName := "string"
		if element.Equal(schema.FloatType) {
			typeName = "float"
		}
		p, err := e.Compile(ctx, `labels:items:map(p=>p[1]):flatmap(v=>groups):map(x=>[x]):map(xs=>xs[0]:[list[`+typeName+`]]:json):exists`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format),
				hffsql.BindJSON(hff.Path{"groups"}, hffsql.Column("records", "groups"), format)))
			wantError := format == hffsql.JSONB && element.Equal(schema.FloatType)
			if (err != nil) != wantError {
				t.Errorf("%s %s: %v", format, typeName, err)
			}
		}
	}
}

// Each family uses the same JSON string class on disk. Only actual HFF
// Strings are eligible as keys; method dispatch also precedes its arguments.
type deferredDispatchCase struct {
	name    string
	keyType hff.Type
	key     hff.Value
	field   string
}

func deferredDispatchCases() []deferredDispatchCase {
	return []deferredDispatchCase{
		{"bytes", schema.Union(schema.BytesType, schema.IntType), hff.Bytes([]byte("a")), "YQ=="},
		{"duration", schema.Union(schema.DurationType, schema.IntType), hff.Duration(time.Nanosecond), "0.000000001s"},
		{"timestamp", schema.Union(schema.TimestampType, schema.IntType), hff.Timestamp(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), "2024-01-01T00:00:00Z"},
		{"string", schema.AnyType, hff.String("YQ=="), "YQ=="},
		{"integer", schema.AnyType, hff.Int(0), "YQ=="},
	}
}

func deferredDispatchSources(tc deferredDispatchCase) []string {
	prefix := `labels:items:map(p=>p[1]):map(v=>`
	choice := `[{"` + tc.field + `":v},[v]][index]`
	sources := []string{
		prefix + choice + `):map(o=>o[key]:type):contains("string")`,
		prefix + choice + `):map(o=>o:count):contains(null)`,
		prefix + choice + `):map(o=>o:get(null!)):exists`,
	}
	if tc.keyType.Kind() == hff.AnyType {
		sources = append(sources,
			prefix+choice+`):map(o=>o:get(key):type):contains("string")`,
			prefix+`{"`+tc.field+`":v}):map(o=>o[key]:type):contains("string")`,
			prefix+`{"`+tc.field+`":v}):map(o=>o:get(key):type):contains("string")`)
	}
	return sources
}

func deferredDispatchSchema(tc deferredDispatchCase) hff.Type {
	return schema.Struct(schema.Map("labels", schema.StringType), schema.Field("key", tc.keyType), schema.Int("index"))
}

func deferredDispatchBindings(format hffsql.JSONFormat) []hffsql.Binding {
	var options []hffsql.JSONBindingOption
	if format == hffsql.JSONB {
		options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "key_meta")))
	}
	return []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format),
		hffsql.BindJSON(hff.Path{"key"}, hffsql.Column("records", "key"), format, options...),
		hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "idx")),
	}
}

func TestDeferredCollectionDispatchBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range deferredDispatchCases() {
		e, err := engine.New(engine.WithSchema(deferredDispatchSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredDispatchSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(deferredDispatchBindings(format)...))
				if err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestDeferredChoicePreservesFindAndTypeObservers(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{schema.BytesType, schema.DurationType, schema.TimestampType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", typ), schema.Field("unknown", schema.AnyType), schema.Int("index"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{`[v,unknown][index]`, `[unknown,v][index]`, `{"a":v,"b":unknown}["a"]`} {
			for _, observer := range []string{`:find(x=>true):type="bytes"`, `:map(x=>x:type):json:exists`} {
				source := `labels:items:map(p=>p[1]):map(v=>` + body + `)` + observer
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.BindJSON(hff.Path{"unknown"}, hffsql.Column("records", "unknown"), hffsql.JSON), hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "idx"))))
				if err != nil {
					t.Errorf("%v %s: %v", typ, source, err)
				}
			}
		}
	}
}

func TestDeferredInapplicableGetStillValidatesArguments(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType), schema.Field("unknown", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, receiver := range []string{`[[v],unknown][0]`, `[{"x":v},null][1]`, `[{"x":v},true][1]`, `[{"x":{"key":v}},{}][1][unknown]`} {
		for _, key := range []string{`("a"~"."):string`, `"\u0000"`, `null!`} {
			source := `labels:items:map(p=>p[1]):map(v=>` + receiver + `:get(` + key + `)):exists`
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.BindJSON(hff.Path{"unknown"}, hffsql.Column("records", "unknown"), hffsql.JSON)))
			if key == `null!` {
				if err != nil {
					t.Fatal(err)
				}
				used := map[int]bool{}
				for _, match := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(query.Where(), -1) {
					i, _ := strconv.Atoi(match[1])
					used[i] = true
				}
				for i := range query.Args() {
					if !used[i+1] {
						t.Errorf("discarded argument%d leaked", i+1)
					}
				}
				continue
			}
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != hff.BackendError {
				t.Errorf("%s: %v", source, err)
			}
		}
	}
}

func TestDeferredCollectionBuildCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):map(v=>{"payload":[v]}):map(o=>o:[map[list[any]]]):flatmap(o=>o.payload):map(v=>v:type):json:exists`,
		`labels:items:map(p=>p[1]):map(v=>[v,v]):map(xs=>xs[labels:size%2]:json):exists`,
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
}

func deferredStructuredLeafFixture() itemRoleFixture {
	object := hff.Object(map[string]hff.Value{"parts": hff.List(hff.Int(1), hff.Int(2))})
	element := schema.NullableType(schema.MapOf(schema.ListOf(schema.IntType)))
	return itemRoleFixture{name: "list-field/map", tag: "object", typ: schema.NullableType(schema.MapOf(element)), first: object, second: object, zero: hff.Object(nil)}
}
func deferredStructuredLeafSources(itemRoleFixture) []string {
	return []string{
		`labels:items:map(p=>[p[1]]):map(x=>x[0]:get("parts")):flatmap(x=>x):map(x=>x:type):json:exists`,
		`labels:items:map(p=>{"payload":p[1]}):map(x=>x.payload:get("parts")):map(x=>x[0]):map(x=>x:type):json:exists`,
	}
}
func deferredFailureSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	var sources []string
	for _, wrapper := range []string{`:map(v=>{"payload":v}):flatmap(o=>o.payload:get(o.payload.key!))`, `:map(v=>[v]):flatmap(xs=>xs[0]:get(xs[0].key!))`} {
		for _, observer := range []string{`:map(x=>x:type):exists`, `:filter(x=>false):all(x=>true)=null`, `:flatmap(x=>[x!]):map(x=>x:type)=null`} {
			sources = append(sources, values+wrapper+observer)
		}
	}
	return sources
}

func deferredFoldedConstructorSources() []string {
	var sources []string
	for _, value := range []string{`"1":number`, `"1s":duration`, `"YQ==":frombase64`, `"invalid":number`} {
		sources = append(sources, `labels:items:map(p=>p[1]):map(v=>[v,`+value+`,unknown][1]:get(null!)):exists`)
	}
	return sources
}

func TestDeferredFoldedConstructorParametersRemainBound(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType), schema.Field("unknown", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range deferredFoldedConstructorSources() {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.BindJSON(hff.Path{"unknown"}, hffsql.Column("records", "unknown"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		used := map[int]bool{}
		for _, match := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(q.Where(), -1) {
			i, _ := strconv.Atoi(match[1])
			used[i] = true
		}
		for i := range q.Args() {
			if !used[i+1] {
				t.Errorf("%s unused argument%d", source, i+1)
			}
		}
	}
}
