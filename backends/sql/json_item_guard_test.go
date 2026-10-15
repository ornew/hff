package sql_test

import (
	"context"
	"testing"

	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

func itemGuardSources(tc itemRoleFixture) []string {
	sources := []string{
		`labels:items[0]:[list]:exists`,
		`labels:items[0]:[list[string]]:exists`,
		`labels:items[0]:[list[string|null]]:exists`,
		`labels:items[0]:[list[bytes]]:exists`,
		`labels:items[0]:[list[bytes]][0]:base64=null`,
		`labels:items[0]:[list[string]][1]:len>0`,
		`labels:items[0]:[list[string]]:[list[string]][1]:type="string"`,
		`labels:items[0]:[list[string]|null]=null`,
		`!labels:items[0]:[list[string]]:exists`,
		`labels!:items[0]!:[list]:exists`,
		`labels:items:[list[list]]:json=items_json`,
		`labels:items:[list[list[string]]]:exists`,
		`labels:items:[list[list[string|null]]]:exists`,
		`labels:items:[list[list[bytes]]]:exists`,
		`labels:items:[list[list[string]]]:map(p=>p[0]):json=keys_json`,
		`labels:items:[list[list[string]]]:map(p=>p[1]):json=values_json`,
		`labels:items:map(p=>p:[list[string]][1]:len):size=labels:size`,
		`labels:items:filter(p=>p:[list[string]][1]:len>0):size>0`,
		`labels:items:map(p=>p:[list]):json=items_json`,
		`labels:items:flatmap(p=>[p:[list]]):json=items_json`,
		`labels:items:flatmap(p=>[p,null]):[list[list|null]]:json!:exists`,
		`labels:items:flatmap(p=>[p,null]):[list[list[bytes]|null]]:json!:exists`,
		`labels:items:[list[list[bytes]]]:json="[]"`,
		`labels:items:[list[list]|null]=null`,
		`labels:items:foldl(null,(a,p)=>p:[list[string]]):json!:exists`,
		`labels:items[0]:[list[string]|list[bytes]][1]:type="string"`,
		`labels:items[0]:[null]:[list]:exists`,
		`labels:items:[null]:[list[list]]:exists`,
		`labels:items[0]:[null]:[list|null]=null`,
		`labels:items[0]:[list|string][0]="app"`,
		`labels:items:[list[list[string|null]]]:foldl([], (a,p)=>a:concat([p])):json=items_json`,
		`labels:items:[list[list[string]]]:concat(other:items:[list[list[string]]]):unique`,
		`labels:items:any(p=>other:items:any(q=>q:[list[string]][0]=p:[list[string]][0]))`,
		`labels:items:any(p=>other:items:any(p=>p:[list[string]][0]="app") p:[list[string]][0]="app")`,
	}
	if !tc.mixed {
		sources = append(sources,
			`labels:items[0]:[list[string|`+tc.tag+`|null]]:json=first_json`,
			`labels:items:[list[list[string|`+tc.tag+`|null]]]:json=items_json`,
		)
	}
	return sources
}

func TestJSONItemGuardsBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemRoleFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemGuardSources(tc) {
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
