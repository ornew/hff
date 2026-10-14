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

var foldedNullCases = []struct {
	source, carrier string
}{
	{`"bad":bool`, "boolean"},
	{`"bad":frombase64`, "bytea"},
	{`"bad":frombase64r`, "bytea"},
	{`"bad":duration`, "bigint"},
	{`"bad":timestamp`, "numeric"},
	{`"9223372036854775808":number`, "json"},
	{`"01":number`, "json"},
	{`"01":number:round`, "json"},
	{`null:round`, "unknown"},
	{`null:bool`, "unknown"},
	{`null:duration`, "unknown"},
	{`null:timestamp`, "unknown"},
}

func foldedNullExtension(t *testing.T) (*engine.Engine, hff.MethodDeclaration) {
	t.Helper()
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{
		ID: "test.nullcarrier.v1", Name: "nullcarrier", Receiver: schema.StringType,
		Parameters: []hff.Type{schema.AnyType}, Result: schema.BoolType,
		NullPolicy: hff.InvokeNull, BaseCost: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"))), engine.WithMethods(d),
		engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	return e, d
}

func foldedNullQuery(t *testing.T, e *engine.Engine, d hff.MethodDeclaration, source, carrier string) *hffsql.Query {
	t.Helper()
	p, err := e.Compile(context.Background(), `name:nullcarrier(`+source+`)`)
	if err != nil {
		t.Fatal(source, err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))),
		hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL,
			func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
				// The function needs the argument's original SQL carrier even
				// though its HFF value is ordinary null.
				return c.Expression(schema.BoolType, "pg_typeof({})::text='"+carrier+"'", args[0])
			})))
	if err != nil {
		t.Fatal(source, err)
	}
	return q
}

func TestFoldedNullRetainsExtensionCarrier(t *testing.T) {
	e, d := foldedNullExtension(t)
	for _, tc := range foldedNullCases {
		q := foldedNullQuery(t, e, d, tc.source, tc.carrier)
		if len(q.Args()) != 0 {
			t.Fatal(tc.source, "retained unused conversion input", q.Args())
		}
		want := "pg_typeof(NULL::" + tc.carrier + ")"
		if tc.carrier == "unknown" {
			want = "pg_typeof(NULL)"
		}
		if !strings.Contains(q.Where(), want) {
			t.Errorf("%s lost SQL carrier %s: %s", tc.source, tc.carrier, q.Where())
		}
	}
}
