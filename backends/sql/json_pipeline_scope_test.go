package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func pipelineNullCarrierQuery(t *testing.T, method string) *hffsql.Query {
	t.Helper()
	_, declaration := foldedNullExtension(t)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.NullableType(schema.StringType)))), engine.WithMethods(declaration), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):filter(v=>v=null):map(v=>v:[null]:`+method+`):map(v=>"x":nullcarrier(v)):all(v=>v)`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(declaration, hffsql.PostgreSQL,
		func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "pg_typeof({})::text='unknown'", args[0])
		})))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestPipelineStageRetainsNullExtensionCarrier(t *testing.T) {
	for _, method := range []string{"bool", "round", "duration", "timestamp"} {
		q := pipelineNullCarrierQuery(t, method)
		if !strings.Contains(q.Where(), "pg_typeof(NULL)") {
			t.Errorf("%s projected its untyped null carrier", method)
		}
	}
}
