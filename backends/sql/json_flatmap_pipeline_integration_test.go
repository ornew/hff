//go:build integration

package sql_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLJSONFlatmapPipelinesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemFlatmapPipelineSources, flatmapPipelineFixtures()...)
}

func TestPostgreSQLJSONFlatmapFailurePipelinesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, flatmapFailureSources, flatmapFailureFixture())
}

// PostgreSQL type-checks expressions even in zero-row scopes. Never must use
// the same untyped NULL carrier as ordinary empty-list callback parameters.
func TestPostgreSQLJSONFlatmapEmptyExtensionScopes(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	number := schema.NullableType(schema.IntType)
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
		ID: "example.empty.plus", Name: "plus", Receiver: schema.StringType,
		Parameters: []hff.Type{number}, Result: number, NullPolicy: hff.InvokeNull, BaseCost: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))),
		engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))), engine.WithMethods(declaration))
	if err != nil {
		t.Fatal(err)
	}
	implementation := hffsql.Implement(declaration, hffsql.PostgreSQL, func(call *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
		return call.Expression(number, "({}+1)", args[0])
	})
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, suffix := range []string{
		`:map(v=>"anchor":plus(v)):size=0`,
		`:filter(v=>"anchor":plus(v)=null):size=0`,
		`:flatmap(v=>v):map(v=>"anchor":plus(v)):size=0`,
	} {
		source := `labels:items:map(p=>p[1]):flatmap(v=>[])` + suffix
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(implementation))
		if err != nil {
			t.Fatal(source, err)
		}
		appendAggregateProfileQuery(t, &script, q, `(VALUES ('{"app":"auth"}'::json)) records(labels)`, len(expected))
		expected = append(expected, true)
		labels = append(labels, source)
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
