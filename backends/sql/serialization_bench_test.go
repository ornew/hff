package sql_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type serializationWorkload struct {
	name, source, fixture string
	typ                   hff.Type
}

func serializationWorkloads() []serializationWorkload {
	return []serializationWorkload{
		{"TypedStringControl", `value:json=expected`, "string", schema.StringType},
		{"TypedListControl", `value:json=expected`, "strings", schema.ListOf(schema.StringType)},
		{"AnyScalar", `value:json=expected`, "scalar", schema.AnyType},
		{"AnyNested", `value:json=expected`, "nested", schema.AnyType},
		{"AnyWide", `value:json=expected`, "wide", schema.AnyType},
		{"AnyDeep", `value:json=expected`, "deep", schema.AnyType},
		{"Callback", `value:map(x=>x):json=expected`, "list", schema.ListOf(schema.AnyType)},
	}
}

func serializationEngine(tb testing.TB, w serializationWorkload) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", w.typ), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func serializationOptions(format hffsql.JSONFormat) []hffsql.Option {
	binding := hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format)
	if format == hffsql.JSONB {
		binding = hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	}
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding, hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected")))}
}

func serializationFixture(kind string, i int) hff.Value {
	number := hff.Float(float64(i%5) + 0.5)
	if i%11 == 0 {
		number = hff.Float(math.Copysign(0, -1))
	}
	scalar := []hff.Value{hff.Null(), hff.Int(9007199254740993), number, hff.String("Tokyo"), hff.Bool(false)}[i%5]
	switch kind {
	case "string":
		return hff.String("Tokyo")
	case "strings":
		return hff.List(hff.String("Tokyo"), hff.String("Kyoto"))
	case "scalar":
		return scalar
	case "nested":
		return hff.Object(map[string]hff.Value{"z": hff.List(number, hff.Null(), scalar), "a": hff.String("Tokyo")})
	case "list":
		return hff.List(number, hff.Int(1), hff.Null(), hff.Object(map[string]hff.Value{"z": scalar, "a": hff.String("Tokyo")}))
	case "wide":
		values := map[string]hff.Value{}
		for k := range 64 {
			values[fmt.Sprintf("key%02d", k)] = hff.Int(int64(k))
			if k%4 == 0 {
				values[fmt.Sprintf("key%02d", k)] = number
			}
		}
		return hff.Object(values)
	case "deep":
		value := number
		for depth := range 16 {
			if depth%2 == 0 {
				value = hff.List(value, hff.Null())
			} else {
				value = hff.Object(map[string]hff.Value{"z": value, "a": hff.Int(1)})
			}
		}
		return value
	}
	panic("unknown fixture")
}

func BenchmarkPostgreSQLSerializationBuild(b *testing.B) {
	ctx := context.Background()
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		b.Run(string(format), func(b *testing.B) {
			for _, w := range serializationWorkloads() {
				b.Run(w.name, func(b *testing.B) {
					e := serializationEngine(b, w)
					p, err := e.Compile(ctx, w.source)
					if err != nil {
						b.Fatal(err)
					}
					options := serializationOptions(format)
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
		})
	}
}
