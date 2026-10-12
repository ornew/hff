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

type sortWorkload struct {
	name, source, fixture string
	typ                   hff.Type
}

func sortWorkloads() []sortWorkload {
	return []sortWorkload{
		{"TypedIntControl", `value:sort[0]<0`, "integers", schema.ListOf(schema.IntType)},
		{"TypedFloatControl", `value:sort[0]<0`, "floats", schema.ListOf(schema.FloatType)},
		{"AnyNumbers", `value:sort[0]<0`, "numbers", schema.AnyType},
		{"AnyStrings", `value:sort[0]="first"`, "strings", schema.AnyType},
		{"AnyInvalidNested", `value:sort=null`, "nested", schema.AnyType},
		{"AnyInvalidMixed", `value:sort=null`, "mixed", schema.AnyType},
		{"RepeatedSort", `value:sort:sort[0]<0`, "numbers", schema.AnyType},
	}
}
func sortEngine(tb testing.TB, w sortWorkload) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("value", w.typ)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}
func sortOptions(format hffsql.JSONFormat) []hffsql.Option {
	var options []hffsql.JSONBindingOption
	if format == hffsql.JSONB {
		options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	}
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format, options...))}
}
func sortFixture(kind string, i, size int) hff.Value {
	if i%17 == 0 {
		return hff.Null()
	}
	if (kind == "nested" || kind == "mixed") && i%7 == 0 {
		return hff.List()
	}
	values := make([]hff.Value, size)
	for k := range values {
		n := int64(size - k)
		switch kind {
		case "integers":
			values[k] = hff.Int(n)
		case "floats":
			values[k] = hff.Float(float64(n) + 0.5)
			if k%11 == 0 {
				values[k] = hff.Float(math.Copysign(0, -1))
			}
		case "strings":
			values[k] = hff.String(fmt.Sprintf("item%03d", n))
			if k%7 == 0 {
				values[k] = hff.String(fmt.Sprintf("日%03d", n))
			}
		default:
			if k%2 == 0 {
				values[k] = hff.Int(n)
			} else {
				values[k] = hff.Float(float64(n) + 0.5)
			}
			if k%11 == 0 {
				values[k] = hff.Float(math.Copysign(0, -1))
			}
		}
	}
	if kind == "nested" {
		payload := map[string]hff.Value{}
		for k := range size {
			payload[fmt.Sprintf("key%03d", k)] = hff.Float(float64(k) + 0.5)
		}
		values[0] = hff.Object(map[string]hff.Value{"unused": hff.Object(payload), "name": hff.String("Tokyo")})
	} else if kind == "mixed" {
		values[0] = hff.String("wrong-family")
	} else if kind == "strings" {
		if i%7 != 0 {
			values[0] = hff.String("first")
		}
	} else if i%7 != 0 {
		if kind == "floats" {
			values[0] = hff.Float(-0.5)
		} else {
			values[0] = hff.Int(-1)
		}
	}
	return hff.List(values...)
}

func BenchmarkPostgreSQLSortBuild(b *testing.B) {
	ctx := context.Background()
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		b.Run(string(format), func(b *testing.B) {
			for _, w := range sortWorkloads() {
				b.Run(w.name, func(b *testing.B) {
					e := sortEngine(b, w)
					p, err := e.Compile(ctx, w.source)
					if err != nil {
						b.Fatal(err)
					}
					options := sortOptions(format)
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
