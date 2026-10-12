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

type comparisonWorkload struct {
	name, source, fixture string
	typ                   hff.Type
}

func comparisonWorkloads() []comparisonWorkload {
	listQuery := `value=[1,2,null]`
	objectQuery := `value={"price":1,"empty":null}`
	return []comparisonWorkload{
		{"TypedListControl", listQuery, "int-list", schema.NullableType(schema.ListOf(schema.NullableType(schema.IntType)))},
		{"AnyRootControl", listQuery, "any-list", schema.AnyType},
		{"AnyList", listQuery, "any-list", schema.NullableType(schema.ListOf(schema.AnyType))},
		{"AnyMap", objectQuery, "map", schema.NullableType(schema.MapOf(schema.AnyType))},
		{"AnyStruct", objectQuery, "struct", schema.NullableType(schema.Struct(schema.Any("price"), schema.Optional(schema.Any("empty"))))},
	}
}

func comparisonFixture(kind string, i int) hff.Value {
	if i%13 == 0 {
		return hff.Null()
	}
	number := hff.Int(1)
	if kind != "int-list" && i%2 == 0 {
		number = hff.Float(1)
	}
	if kind == "int-list" || kind == "any-list" {
		values := []hff.Value{number, hff.Int(2), hff.Null()}
		switch {
		case i%7 == 0:
			values = values[:2]
		case i%11 == 0:
			values = append(values, hff.Null())
		case kind == "any-list" && i%17 == 0:
			values[1] = hff.Object(map[string]hff.Value{"n": hff.Int(2)})
		}
		return hff.List(values...)
	}
	values := map[string]hff.Value{"price": number, "empty": hff.Null()}
	switch {
	case i%7 == 0:
		delete(values, "empty")
	case i%11 == 0:
		if kind == "map" {
			values["extra"] = hff.Null()
		} else {
			values["price"] = hff.Int(3)
		}
	case i%17 == 0:
		values["price"] = hff.List(hff.Int(1))
	}
	return hff.Object(values)
}

func comparisonEngine(tb testing.TB, workload comparisonWorkload) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", workload.typ))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func comparisonBinding(format hffsql.JSONFormat) hffsql.Binding {
	var options []hffsql.JSONBindingOption
	if format == hffsql.JSONB {
		options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	}
	return hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format, options...)
}

func BenchmarkPostgreSQLComparisonBuild(b *testing.B) {
	ctx := context.Background()
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, workload := range comparisonWorkloads() {
			b.Run(string(format)+"/"+workload.name, func(b *testing.B) {
				e := comparisonEngine(b, workload)
				p, err := e.Compile(ctx, workload.source)
				if err != nil {
					b.Fatal(err)
				}
				options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(comparisonBinding(format))}
				q, err := hffsql.Build(ctx, p, options...)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := hffsql.Build(ctx, p, options...); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(q.Where())), "sql-bytes")
				b.ReportMetric(float64(len(q.Args())), "parameters")
			})
		}
	}
}
