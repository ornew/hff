package sql_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func allUnconstrainedItemGuardSources() []string {
	return []string{
		`labels:items:any(p=>p:[list]:exists)`,
		`labels:items:all(p=>p:[list]:exists)`,
		`labels:items:count(p=>p:[list]:exists)=labels:size`,
		`labels:items:map(p=>p:[list]):json=items_json`,
		`labels:items:map(p=>p:[list]![1]):json=values_json`,
		`labels:items:filter(p=>p:[list]:exists):json=items_json`,
		`labels:items:flatmap(p=>[p:[list]]):json=items_json`,
		`labels:items:find(p=>p:[list]:exists):json=first_json`,
		`labels:items[0]:[list]:json=first_json`,
		`labels:items[-1]:[list]!:exists`,
		`labels:items[0]:[list]:[list]:json=first_json`,
		`labels:items[0]:[list|string]:json=first_json`,
		`labels:items[0]:[list|null]=null`,
		`labels:items:[list[list]]:json=items_json`,
		`labels!:items:[list[list]]:json=items_json`,
		`labels:items:[list[list]]:[list[list]]:json=items_json`,
		`labels:items:[list[list]]:map(p=>p[1]):json=values_json`,
		`labels:items:flatmap(p=>[p,null]):[list[list]]:exists`,
		`labels:items:flatmap(p=>[p,null]):[list[list|null]]:exists`,
		`labels:items:flatmap(p=>[null,p]):map(p=>p:[list]):exists`,
		`labels:items:flatmap(p=>[p,null]):map(p=>p:[list|null]):exists`,
		`labels:items:any(p=>other:items:any(q=>q:[list][0]=p:[list][0]))`,
		`labels:items:any(p=>other:items:any(p=>p:[list][0]="app") p:[list][0]="app")`,
		`labels:items:any(p=>p:[list[string]][1]:type="string")`,
		`labels:items:any(p=>p:[list][1]:[float]:[float]:exists)`,
		`labels:items:foldl(null,(a,p)=>p:[list]):json!:exists`,
		`labels:items:foldr(null,(p,a)=>p:[list]):json!:exists`,
		`labels:items:map(p=>p:[list]):concat(other:items:[list[list]]):unique`,
	}
}

func unconstrainedItemGuardFixtures() []itemRoleFixture {
	return append(itemSlotFixtures(), itemAnyFixtures()...)
}

func rejectedUnconstrainedItemGuard(tc itemRoleFixture, source string) bool {
	return tc.mixed && !tc.metadata && (source == `labels:items:map(p=>p:[list]![1]):json=values_json` || source == `labels:items:[list[list]]:map(p=>p[1]):json=values_json`)
}

func unconstrainedItemGuardSources(tc itemRoleFixture) []string {
	var accepted []string
	for _, source := range allUnconstrainedItemGuardSources() {
		if !rejectedUnconstrainedItemGuard(tc, source) {
			accepted = append(accepted, source)
		}
	}
	return accepted
}

func TestUnconstrainedItemGuardsPreserveBuildBoundaries(t *testing.T) {
	for _, tc := range unconstrainedItemGuardFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range allUnconstrainedItemGuardSources() {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if rejectedUnconstrainedItemGuard(tc, source) {
					// The direct guarded value-slot materialization remains an
					// unsupported boundary for mixed tagged sibling schemas.
					var diagnostic *hff.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != hff.BackendError {
						t.Error("accepted unsupported identity boundary", tc.name, format, source, err)
					}
				} else if err != nil {
					t.Error(tc.name, format, source, err)
				} else {
					requireConcatArgsUsed(t, q)
				}
			}
		}
	}
}

func TestUnconstrainedItemGuardsBindOnlyObservedKeys(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 12; i++ {
		fields = append(fields, schema.Optional(schema.Nullable(schema.Bytes(fmt.Sprintf("label%02d", i)))))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(fields...)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`labels:items:any(p=>p:[list]:exists)`, `labels:items:[list[list]]:exists`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if err != nil {
				t.Fatal(err)
			}
			// Normalizing the original twelve fields still observes their keys.
			// The unconstrained guard must not bind the same keys a second time.
			if len(q.Args()) != len(fields) {
				t.Fatal(source, format, "unexpected key arguments", q.Args())
			}
			requireConcatArgsUsed(t, q)
		}
	}
}
