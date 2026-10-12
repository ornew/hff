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

func TestEmptySortHasNoEmptyOrderClause(t *testing.T) {
	ctx := context.Background()
	p := compile(t, `name:exists=[]:sort:exists`)
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.Where(), "ORDER BY )") {
		t.Fatal("empty inferred element type produced an empty ORDER BY:", q.Where())
	}
}

func TestDynamicSortRequiresRecoverableNumericTypes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		typ    hff.Type
		source string
	}{
		{schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), `data:sort`},
		{schema.AnyType, `data:sort`},
		{schema.ListOf(schema.FloatType), `data:sort:json="[-0.0,1.0]"`},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB)))
		requireCode(t, err, hff.BackendError)
	}
}
