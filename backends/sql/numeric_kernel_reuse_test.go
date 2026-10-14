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

func numericKernelEngine(t testing.TB) *engine.Engine {
	t.Helper()
	choice := schema.Union(schema.StringType, schema.IntType, schema.FloatType, schema.NullType)
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Int("i")), schema.Nullable(schema.Int("j")), schema.Nullable(schema.Float("f")),
		schema.Field("choice", choice), schema.Field("other", choice), schema.Any("data"),
		schema.Field("items", schema.NullableType(schema.ListOf(choice))),
		schema.Field("floats", schema.NullableType(schema.ListOf(schema.NullableType(schema.FloatType)))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func numericKernelOptions(format hffsql.JSONFormat, metadata bool) []hffsql.Option {
	var bindings []hffsql.Binding
	for _, name := range []string{"i", "j", "f"} {
		bindings = append(bindings, hffsql.Bind(hff.Path{name}, hffsql.Column("records", name)))
	}
	for _, name := range []string{"choice", "other", "data", "items", "floats"} {
		var options []hffsql.JSONBindingOption
		if metadata {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
		}
		bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
	}
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)}
}

func TestNumericKernelReuseRetainsScopeAndOperandIdentity(t *testing.T) {
	e := numericKernelEngine(t)
	for _, tc := range []struct {
		source        string
		floors, casts int
	}{
		{`i<f`, 2, 0},
		{`i<f i!=f f>i`, 2, 0},
		{`i<f j<f`, 3, 0},
		{`(i<f | j<f) !(i=f)`, 3, 0},
		{`i!<f i<f!`, 4, 0},
		{`choice=choice`, 2, 1},
		{`choice=other`, 4, 2},
		{`items:any(x=>x=x)`, 2, 1},
		{`floats:any(f=>i<f j<f)`, 3, 1},
		{`floats:any(x=>floats:any(x=>i<x j<x))`, 3, 1},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(context.Background(), p, numericKernelOptions(hffsql.JSON, false)...)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if count := strings.Count(q.Where(), "floor("); count != tc.floors {
			t.Errorf("%s: floor calls=%d, want%d", tc.source, count, tc.floors)
		}
		if count := strings.Count(q.Where(), "pg_input_is_valid("); count != tc.casts {
			t.Errorf("%s: Float validations=%d, want%d", tc.source, count, tc.casts)
		}
	}
}

func TestNumericKernelReusePreservesRepresentationRejections(t *testing.T) {
	e := numericKernelEngine(t)
	for _, source := range []string{`choice=choice`, `data=data`, `items:any(x=>x=x)`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, numericKernelOptions(hffsql.JSONB, false)...)
		requireCode(t, err, hff.BackendError)
		if _, err := hffsql.Build(context.Background(), p, numericKernelOptions(hffsql.JSONB, true)...); err != nil {
			t.Fatal(source, err)
		}
	}
}
