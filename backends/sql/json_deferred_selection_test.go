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

func deferredSelectionSources(tc itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	if tc.name == "nullable-shape/map" {
		return []string{
			values + `:map(x=>[x:get("parts")][0]:filter(v=>true):concat([]):type):contains("null")`,
			values + `:map(x=>[x:get("parts")][0]:filter(v=>false):concat([]):exists):contains(true)`,
			values + `:map(x=>[x:get("parts")][0]:filter(v=>true):concat(null!):type):contains("null")`,
			values + `:map(x=>[]:filter(v=>false):concat([x:get("parts")][0]):type):contains("null")`,
		}
	}
	flat := values + `:flatmap(x=>[x])`
	types := values + `:map(x=>x:type)`
	sources := []string{
		flat + `[0]:type=` + types + `[0]`,
		flat + `[-1]:type=` + types + `[-1]`,
		flat + `[labels:size%3-1]:type=` + types + `[labels:size%3-1]`,
		values + `:filter(x=>x:exists):flatmap(x=>[x])[0]:type=` + values + `:filter(x=>x:exists):map(x=>x:type)[0]`,
		values + `:flatmap(x=>[x,x]):filter(x=>x:exists)[-1]:type=` + values + `:filter(x=>x:exists):map(x=>x:type)[-1]`,
		values + `:map(x=>{"payload":x}):flatmap(x=>[x])[0].payload:type=` + types + `[0]`,
		values + `:map(x=>{"payload":x}):flatmap(x=>[x])[-1]:get("payload"):type=` + types + `[-1]`,
		values + `:map(x=>[x]):flatmap(x=>[x])[0][0]:type=` + types + `[0]`,
		values + `:map(x=>[x]):flatmap(x=>[x])[0][-1]:type=` + types + `[0]`,
		values + `:map(x=>[x]):flatmap(x=>[x])[0]:[list[any]][0]:type=` + types + `[0]`,
		values + `:map(x=>[x]):flatmap(x=>[x])[0]:map(x=>x:type):json:exists`,
		values + `:map(x=>[x]):flatmap(x=>[x])[0]:flatmap(x=>[x,x]):map(x=>x:type):json:exists`,
		values + `:map(x=>{"payload":[x]}):flatmap(x=>[x])[0].payload:map(x=>x:type):json:exists`,
		flat + `[0]:[string]:len>0`,
		flat + `[0]:[any]:json:exists`,
		flat + `[0]!:type:exists`,
		flat + `[null!]:type="null"`,
		flat + `[0.5]:type="null"`,
		flat + `[9223372036854775807]:type="null"`,
		flat + `[-9223372036854775808]:type="null"`,
		flat + `[9223372036854774784.0]:type="null"`,
		flat + `[9223372036854775808.0]:type="null"`,
		flat + `[-9223372036854775808.0]:type="null"`,
		flat + `[1e100]:type="null"`,
		flat + `:filter(x=>false)[0]:type="null"`,
		values + `:map(x=>x!):flatmap(x=>[x])[0]:type:exists`,
		values + `:flatmap(x=>[x!])[0]:type:exists`,
		`other:items:any(p=>` + flat + `[p[0]:len-3]:type="string")`,
		`other:items:any(p=>` + values + `:flatmap(p=>[p])[0]:type="string")`,
		flat + `[0]:type=` + flat + `[0]:type`,
		values + `:map(x=>[x,"sentinel",x]):flatmap(xs=>[xs])[0]:map(x=>x:type):json=[` + types + `[0],"string",` + types + `[0]]:json`,
		values + `:map(x=>[x,"sentinel",x]):flatmap(xs=>[xs])[0]:flatmap(x=>[x,x]):map(x=>x:type):json=[` + types + `[0],` + types + `[0],"string","string",` + types + `[0],` + types + `[0]]:json`,
		values + `:map(x=>[x]):flatmap(xs=>[xs])[0]:map(x=>x!):filter(x=>false):exists`,
		values + `:map(x=>[x]):flatmap(xs=>[xs])[0]:filter(x=>false):concat([]):exists`,
		values + `:map(x=>[x]):flatmap(xs=>[xs])[0]:concat([]:filter(x=>false)):type="list"`,
		flat + `[100]!:type="null"`,
		flat + `[100]:[null]:type="null"`,
		flat + `[100]:[string]:type="null"`,
	}
	if strings.Contains(tc.name, "/any-field") {
		sources = append(sources,
			flat+`[0]:filter(x=>false):type="list"`,
			flat+`[0]:filter(x=>x!):exists`,
			flat+`[0]:filter(x=>false):concat([]):exists`,
			flat+`[0]:filter(x=>false):concat(null!):exists`,
			flat+`[0]:filter(x=>false):concat([]:filter(x=>false)):type="list"`,
		)
	}
	return sources
}

func deferredSelectionFixtures() []itemRoleFixture {
	element := schema.MapOf(schema.NullableType(schema.ListOf(schema.IntType)))
	return append(deferredValueFixtures(), itemRoleFixture{
		name: "nullable-shape/map", tag: "object",
		typ:    schema.NullableType(schema.MapOf(schema.NullableType(element))),
		first:  hff.Object(map[string]hff.Value{"parts": hff.List(hff.Int(1))}),
		second: hff.Object(map[string]hff.Value{"parts": hff.Null()}),
		zero:   hff.Object(nil),
	})
}

func TestDeferredRowSelectionsBuild(t *testing.T) {
	ctx := context.Background()
	placeholder := regexp.MustCompile(`\$(\d+)`)
	for _, tc := range deferredSelectionFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredSelectionSources(tc) {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Error(tc.name, format, source, err)
					continue
				}
				used := map[int]bool{}
				for _, match := range placeholder.FindAllStringSubmatch(query.Where(), -1) {
					i, _ := strconv.Atoi(match[1])
					used[i] = true
				}
				for i := range query.Args() {
					if !used[i+1] {
						t.Errorf("%s %s unused argument%d: %s", tc.name, format, i+1, source)
					}
				}
			}
		}
	}
}

func TestDeferredRowSelectionPreservesFloatLoss(t *testing.T) {
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
		source := `labels:items:map(p=>p[1]):flatmap(v=>groups)[0]:[list[` + typeName + `]]:json:exists`
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format), hffsql.BindJSON(hff.Path{"groups"}, hffsql.Column("records", "groups"), format)))
			reject := format == hffsql.JSONB && element.Equal(schema.FloatType)
			if (err != nil) != reject {
				t.Errorf("%s %s reject%v: %v", format, typeName, reject, err)
			}
		}
	}
}

func TestDeferredRowSelectionBuildCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(x=>[x])[-1]:type="bytes"`,
		`labels:items:map(p=>[p[1]]):flatmap(x=>[x])[0]:flatmap(x=>[x,x]):map(x=>x:type):exists`,
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
			query, err := hffsql.Build(ctx, p, options...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if query != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("checkpoint%d partial%v err%v", after, query, err)
			}
		}
	}
}

func TestDeferredRowSelectionRetainsBuildDiagnostics(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(x=>[x])[100]:[string]:matches(".")`,
		`labels:items:map(p=>p[1]):flatmap(x=>[x])[100]:[string]:concat("\u0000"):exists`,
		`labels:items:map(p=>p[1]):flatmap(x=>[x])[("a"~"."):string:len]:exists`,
		`labels:items:map(p=>[p[1]]):flatmap(x=>[x])[0]:filter(x=>x:matches(".")):exists`,
		`labels:items:map(p=>[p[1]]):flatmap(x=>[x])[0]:filter(x=>x="\u0000"):concat([]):type="list"`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		requireCode(t, err, hff.BackendError)
	}
}
