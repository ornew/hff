package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

func itemMapPipelineSources(tc itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	return []string{
		values + `:map(v=>v:type):contains("` + tc.tag + `")`,
		values + `:any(v=>v:type="` + tc.tag + `")`,
		values + `:all(v=>v:type="` + tc.tag + `")`,
		values + `:count(v=>v:type="` + tc.tag + `")>0`,
		values + `:any(v=>v)`,
		values + `:all(v=>v)`,
		values + `:count(v=>v)>0`,
		values + `:map(v=>!v):contains(true)`,
		values + `:map(v=>v:[any]):map(v=>v:type):contains("` + tc.tag + `")`,
		values + `:map(v=>v:[string]:len):size=labels:size`,
		values + `:map(v=>v:[number]+1):map(v=>v:type):exists`,
		values + `:map(v=>v:[null]):any(v=>true)`,
		values + `:map(v=>v:[string]):all(v=>true)`,
		values + `:map(v=>v:[null]):count(v=>true)=0`,
		values + `:map(v=>v:[null]):map(v=>1):exists`,
		values + `:any(v=>v:[null]:exists | v:[string]:exists)`,
		values + `:all(v=>v:[string]:exists | v:[null]:exists)`,
		values + `:map(v=>[v:[string]:exists,v:[null]:exists]):json:exists`,
		values + `:map(v=>[v:[null]:exists,v:[string]:exists]):json:exists`,
		values + `:map(v=>other:items:any(q=>q[1]:type=v:type)):all(v=>v)`,
		values + `:map(v=>other:items:map(v=>v[1]):any(v=>v:type="` + tc.tag + `") v:exists):all(v=>v)`,
		`labels:items:map(p=>p):map(p=>p[1]):map(v=>v:type):contains("` + tc.tag + `")`,
		`labels:items:map(p=>p[-1]):map(v=>v:type):contains("` + tc.tag + `")`,
		`labels:items:map(p=>p[labels:size-1]):map(v=>v:type):contains("string")`,
		`labels:items:map(p=>p[labels!:size-1]):map(v=>v:[any]):any(v=>v:exists)`,
		`labels!:items:map(p=>p[1]):any(v=>v:type="` + tc.tag + `")`,
		`labels:items:map(p=>p[1]!):any(v=>true)`,
		`labels:items:map(p=>p[1]!):all(v=>true)`,
		`labels:items:map(p=>p[1]!):count(v=>true)>0`,
		`labels:items:map(p=>p[1]!):map(v=>true):exists`,
		`!labels:items:map(p=>p[1]!):any(v=>true)`,
		`labels:items:map(p=>p[1]!):any(v=>true)=null`,
		`labels:items:map(p=>p[1]!):map(v=>true):type="null"`,
		`labels:items:map(p=>p[1]:[null]):map(v=>v:[any]):all(v=>v:type="null")`,
	}
}

func itemMapPipelineFixtures() []itemRoleFixture {
	fixtures := append(itemSlotFixtures(), itemAnyFixtures()...)
	for i := range fixtures {
		fixture := &fixtures[i]
		app, otherApp := fixture.first, hff.Null()
		if fixture.mixed {
			app, otherApp = hff.String("auth"), hff.String("auth")
		}
		// A match in the first item cannot hide a later map guard failure.
		fixture.extraPairs = append(fixture.extraPairs, [2]hff.Value{
			hff.Object(map[string]hff.Value{"app": app, "z": hff.Null()}),
			hff.Object(map[string]hff.Value{"app": otherApp, "z": fixture.second}),
		})
	}
	return fixtures
}

func TestJSONItemMapPipelinesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemMapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range append(itemMapPipelineSources(tc), itemFilteredMapPipelineSources(tc)...) {
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

func itemFilteredMapPipelineSources(tc itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	return []string{
		values + `:filter(v=>v:exists):map(v=>v:type)[0]="string"`,
		values + `:filter(v=>v:exists):map(v=>v:type):contains("` + tc.tag + `")`,
		values + `:filter(v=>v:exists):any(v=>v:type="` + tc.tag + `")`,
		values + `:filter(v=>v:exists):all(v=>v:exists)`,
		values + `:filter(v=>v:exists):count(v=>v:type="` + tc.tag + `")>0`,
		values + `:filter(v=>v!=null):any(v=>v:exists)`,
		values + `:filter(v=>v=null):map(v=>v:type):contains("null")`,
		values + `:filter(v=>v=null):any(v=>v:exists)`,
		values + `:filter(v=>v:[string]:len>0):map(v=>v:type):contains("string")`,
		values + `:filter(v=>v:[string]:exists):map(v=>v:[string]:len):exists`,
		values + `:filter(v=>v:[null]:type="null"):all(v=>v=null)`,
		values + `:filter(v=>v:[null]:exists | v:[string]:exists):map(v=>v:type):exists`,
		values + `:filter(v=>true):filter(v=>false):map(v=>v!:type):exists`,
		values + `:filter(v=>false):map(v=>v!):any(v=>true)`,
		values + `:filter(v=>false):map(v=>v!):all(v=>false)`,
		values + `:filter(v=>false):map(v=>v!):count(v=>true)=0`,
		values + `:map(v=>v!):filter(v=>false):map(v=>true):exists`,
		values + `:map(v=>v!):filter(v=>false):any(v=>true)`,
		values + `:map(v=>v!):filter(v=>false):all(v=>true)`,
		values + `:map(v=>v!):filter(v=>false):count(v=>true)=0`,
		values + `:filter(v=>v:exists):map(v=>v!):filter(v=>false):map(v=>true):exists`,
		values + `:filter(v=>v):map(v=>v!):any(v=>true)`,
		values + `:filter(v=>other:items:any(q=>q[1]:type=v:type)):map(v=>v:type):exists`,
		values + `:filter(v=>other:items:any(v=>v[1]:exists) v:exists):map(v=>v:type):exists`,
		`labels:items:filter(p=>p[0]="app"):map(p=>p[1]):filter(v=>v:exists):map(v=>v:type):contains("string")`,
		`labels:items:map(p=>p[labels:size-1]):filter(v=>v:exists):map(v=>v:type):contains("string")`,
	}
}
