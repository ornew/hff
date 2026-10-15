package sql_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func normalizedGuardSources(name string) []string {
	targets := map[string]string{
		"Int": "int", "Float": "float", "String": "string", "Bool": "bool",
		"Bytes": "bytes", "Duration": "duration", "Timestamp": "timestamp",
		"NumericUnion": "number", "Container": "list[float|null]", "Any": "any",
		"Map": "map[float|null]", "Struct": "map[number]", "ObjectUnion": "map[number]",
	}
	target := targets[name]
	if target == "" {
		panic("missing guard target: " + name)
	}
	guard := ":[list[" + target + "|null]]"
	return []string{
		`items:map(x=>x)` + guard + `:json="[]"`,
		`items:filter(x=>true)` + guard + `:json="[]"`,
		`items:map(x=>x)` + guard + guard + `:json="[]"`,
		`items:map(x=>x)` + guard + `:[list]:json="[]"`,
		`items:map(x=>x)` + guard + `:map(x=>x):json="[]"`,
		`[items:map(x=>x)` + guard + `]:json="[]"`,
		`items:map(x=>needle)` + guard + `:json="[]"`,
		`items:map(x=>items:map(y=>x)` + guard + `):json="[]"`,
		`items:map(x=>items:map(x=>x)` + guard + `):json="[]"`,
	}
}

func impossibleItemGuardSources(itemRoleFixture) []string {
	return []string{
		`labels:items:any(p=>p:[list[string]]:exists)`,
		`labels:items:all(p=>p:[list[string]]:exists)`,
		`labels:items:count(p=>p:[list[string]]:exists)=labels:size`,
		`labels:items:filter(p=>p:[list[string]]:exists):size=0`,
		`labels:items:[list[list[string]]]:exists`,
		`labels:items:[list[list[string]]]:[list[list]]:exists`,
		`labels:items[0]:[list[string]]:exists`,
		`labels:items[0]:[list[null]]:exists`,
		`labels:items:map(p=>p:[list[string]]):exists`,
		`labels:items:flatmap(p=>[p,null]):[list[list[string]|null]]:exists`,
		`labels:items:flatmap(p=>[p,null]):[list[list[string]]]:exists`,
		`labels:items:any(p=>other:items:any(q=>q:[list[string]][0]=p[0]))`,
		`labels:items:any(p=>other:items:any(p=>p:[list[string]]:exists) p[0]="app")`,
		`labels:items:any(p=>p:[list[number]]:[list[null]]:exists)`,
	}
}

func TestImpossibleItemGuardsBuild(t *testing.T) {
	for _, fixture := range unconstrainedItemGuardFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(fixture)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range impossibleItemGuardSources(fixture) {
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

func TestImpossibleItemGuardBindsOnlyNormalizedKeys(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 12; i++ {
		fields = append(fields, schema.Optional(schema.Nullable(schema.Bytes(fmt.Sprintf("label%02d", i)))))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(fields...)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:any(p=>p:[list[string]]:exists)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
		if err != nil {
			t.Fatal(err)
		}
		if len(q.Args()) != len(fields) {
			t.Fatal(format, q.Args())
		}
		if strings.Contains(q.Where(), "THEN FALSE WHEN") {
			t.Fatal("constant key dispatch remains")
		}
		requireConcatArgsUsed(t, q)
	}
}

func TestNormalizedContainerGuardsAvoidRepeatedNormalization(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("items", schema.NullableType(schema.ListOf(schema.NullableType(schema.FloatType)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source     string
		traversals int
	}{
		{`items:map(x=>x):[list[float|null]]:json="[]"`, 4},
		{`items:filter(x=>x!=null):[list[float|null]]:json="[]"`, 5},
		{`items:map(x=>x):[list[number|null]]:json="[]"`, 4},
		{`items:map(x=>x):[list[float|null]|null]:json="[]"`, 4},
		// Raw input has no producer proof: recovery normalization remains.
		{`items:[list[float|null]]:json="[]"`, 9},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var opts []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "items_meta")))
			}
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, opts...)))
			if err != nil {
				t.Fatal(tc.source, format, err)
			}
			got := strings.Count(q.Where(), "json_array_elements(") + strings.Count(q.Where(), "jsonb_array_elements(")
			want := tc.traversals
			if format == hffsql.JSONB {
				// Recover stored numeric subtypes from the caller's metadata.
				want++
			}
			if got != want {
				t.Fatal(tc.source, format, "traversal sites", got, want)
			}
			requireConcatArgsUsed(t, q)
		}
	}
}
