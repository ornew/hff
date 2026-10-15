package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func itemPostConcatPipelineSources(tc itemRoleFixture) []string {
	left := `labels:items:map(p=>p[1])`
	right := `other:items:map(p=>p[1])`
	joined := left + `:concat(` + right + `)`
	return []string{
		joined + `:map(v=>v:[any]):map(v=>v:type):json=` + joined + `:map(v=>v:type):json`,
		joined + `:filter(v=>v:type="` + tc.tag + `"):map(v=>v:type):all(v=>v="` + tc.tag + `")`,
		joined + `:map(v=>v!):filter(v=>false):all(v=>true)`,
		joined + `:map(v=>v!):filter(v=>false):all(v=>true)=null`,
		joined + `:filter(v=>false):map(v=>v!):all(v=>true)`,
		joined + `:filter(v=>v:[string]:exists):map(v=>v:type):all(v=>v="string")`,
		joined + `:map(v=>v:[string]):filter(v=>false):count(v=>true)=0`,
		joined + `:map(v=>v:[string]):filter(v=>false):map(v=>v:type):type="null"`,
		joined + `:filter(v=>v:exists):map(v=>v:type):map(v=>v:len):all(n=>n>0)`,
		joined + `:map(v=>other:items:any(p=>p[1]:type=v:type)):filter(v=>v):map(v=>v:type):contains("bool")`,
		`labels:items:concat(other:items):map(p=>p[1]):filter(v=>v:exists):map(v=>v:type):contains("` + tc.tag + `")`,
		`labels:items:concat(other:items):filter(p=>p[0]="app"):map(p=>p[labels:size-1]):map(v=>v:type):contains("string")`,
		joined + `:map(v=>v:[null]):map(v=>1):any(v=>true)`,
		joined + `:filter(v=>v:type="null"):map(v=>v:[null]):map(v=>v:type):all(v=>v="null")`,
		joined + `:map(v=>v:json):map(v=>v:len):count(v=>v>0)>0`,
		joined + `:map(v=>v:type):concat(` + left + `):map(v=>v:type):count(v=>v="string")>0`,
		joined + `:map(v=>v:type):concat(` + right + `):filter(v=>v:type="string"):map(v=>v:type):all(v=>v="string")`,
		left + `:concat(` + joined + `:map(v=>v:type)):map(v=>v:type):json:exists`,
		`labels:items:map(p=>p[1]!):concat(` + joined + `:map(v=>v:type)):all(v=>true)=null`,
	}
}

func TestJSONPostConcatPipelinesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemMapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemPostConcatPipelineSources(tc) {
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

func TestPostConcatBuildAdvancesSharedStagesAcrossAllArms(t *testing.T) {
	ctx := context.Background()
	var declarations []hff.MethodDeclaration
	var implementations []hffsql.MethodImplementation
	var visited []string
	set := capabilities.Standard()
	for _, name := range []string{"first", "second"} {
		d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.pipeline." + name, Name: "stage" + name, Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1})
		if err != nil {
			t.Fatal(err)
		}
		declarations = append(declarations, d)
		set = set.Allow(capabilities.Method(d.ID()))
		implementations = append(implementations, hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			visited = append(visited, name)
			return c.Expression(schema.StringType, "{}", receiver)
		}))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType), schema.Map("other", schema.StringType))), engine.WithMethods(declarations...), engine.WithCapabilities(set))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `labels:items:map(p=>p[1]):concat(other:items:map(p=>p[1])):map(v=>v:stagefirst):map(v=>v:stagesecond):any(v=>v)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithMethods(implementations...), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.BindJSON(hff.Path{"other"}, hffsql.Column("records", "other"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	firstSeen, secondSeen := false, false
	for _, name := range visited {
		if name == "first" {
			if secondSeen {
				t.Fatalf("earlier stage translated after a later stage: %v", visited)
			}
			firstSeen = true
		} else {
			secondSeen = true
		}
	}
	if !firstSeen || !secondSeen {
		t.Fatalf("missing shared stage translation: %v", visited)
	}
}
