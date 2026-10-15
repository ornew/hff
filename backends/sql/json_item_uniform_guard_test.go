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

func uniformItemGuardSources(tc itemRoleFixture) []string {
	sources := impossibleItemGuardSources(tc)
	for i, source := range sources {
		sources[i] = strings.ReplaceAll(source, "list[string]", "list[string|null]")
	}
	return append(sources,
		`labels:items:any(p=>p:[list[string|null]]:[list[string|null]]:exists)`,
		`labels:items:any(p=>p:[list[string|null]][1]=null)`,
		`labels:items:any(p=>!(p:[list[string|null]][1]=null))`,
		`labels:items:find(p=>p:[list[string|null]]:exists):exists`,
		`labels:items:any(p=>p[1]:[string|null]:exists)`,
		`labels:items:any(p=>p[-1]:[string|null]:exists)`,
		`labels:items:any(p=>p[0]:[string|null]:exists)`,
		`labels:items:any(p=>p[-2]:[string|null]:exists)`,
		`labels:items:any(p=>p[2]:[string|null]:exists)`,
		`labels:items:any(p=>p[-3]:[string|null]:exists)`,
		`labels:items:any(p=>p[1.5]:[string|null]:exists)`,
		`labels:items:any(p=>p[null]:[string|null]:exists)`,
		`labels:items:any(p=>p[labels:size%3]:[string|null]:exists)`,
		`labels:items:any(p=>p[labels:size:[null]]:[string|null]:exists)`,
		`labels:items:any(p=>p[1]:[any]:[string|null]:exists)`,
		`labels:items:any(p=>p[1]:[number]:[string|null]:exists)`,
		`labels:items:any(p=>p[1]:[null]=null)`,
		`labels:items:all(p=>p[1]:[string|null]:exists)`,
		`labels:items:filter(p=>p[1]:[string|null]:exists):size>0`,
		`labels:items:map(p=>p:[list[string|null]][1]):json:exists`,
	)
}

func TestUniformItemGuardsBuild(t *testing.T) {
	for _, fixture := range unconstrainedItemGuardFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(fixture)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range uniformItemGuardSources(fixture) {
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

func TestUniformItemGuardsDoNotRebindUnobservedKeys(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 12; i++ {
		fields = append(fields, schema.Optional(schema.Nullable(schema.Bytes(fmt.Sprintf("label%02d", i)))))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(fields...)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:any(p=>p:[list[string|null]]:exists)`,
		`labels:items:all(p=>p:[list[string|null]]:exists)`,
		`labels:items:[list[list[string|null]]]:exists`,
		`labels:items:any(p=>p[1]:[string|null]:exists)`,
		`labels:items:any(p=>p[1]:[null]=null)`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if err != nil {
				t.Fatal(source, format, err)
			}
			want := 12
			if strings.Contains(source, "p[1]") {
				want++
			}
			if len(q.Args()) != want {
				t.Fatal(source, format, q.Args())
			}
			requireConcatArgsUsed(t, q)
		}
	}
}
