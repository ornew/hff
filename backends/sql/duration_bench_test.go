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

type durationWorkload struct {
	name, source string
	digits       int
}

func durationWorkloads() []durationWorkload {
	return []durationWorkload{
		{"StringControl", `value:len>0`, 0},
		{"IntegerUnits", `value:duration:nanoseconds>0`, 0},
		{"CompoundUnits", `value:duration:nanoseconds>0`, -1},
		{"Fraction16", `value:duration:nanoseconds>0`, 16},
		{"Fraction512", `value:duration:nanoseconds>0`, 512},
		{"Fraction4096", `value:duration:nanoseconds>0`, 4096},
		{"Repeated512", `value:duration:nanoseconds>0 value:duration:seconds>0`, 512},
		{"Serialized512", `value:duration:string="0.000000001s"`, 512},
		{"Fraction20000", `value:duration:nanoseconds>0`, 20000},
	}
}

func durationEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.String("value")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func durationOptions(format hffsql.JSONFormat) []hffsql.Option {
	binding := hffsql.Bind(hff.Path{"value"}, hffsql.Column("records", "value"))
	if format != "" {
		binding = hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format)
	}
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)}
}

func durationFixture(w durationWorkload, i int) hff.Value {
	if i%17 == 16 {
		return hff.Null()
	}
	if i%7 == 6 {
		return hff.String("invalid")
	}
	if i%11 == 10 {
		return hff.String("0")
	}
	text := "1s"
	if w.digits < 0 {
		text = "1h30m.5s"
	} else if w.digits > 0 {
		text = "0." + strings.Repeat("0", w.digits-1) + "1ns0." + strings.Repeat("9", w.digits) + "ns"
	}
	if i%2 != 0 {
		text = "-" + text
	}
	return hff.String(text)
}

func BenchmarkPostgreSQLDurationBuild(b *testing.B) {
	ctx := context.Background()
	for _, format := range []hffsql.JSONFormat{"", hffsql.JSON, hffsql.JSONB} {
		label := string(format)
		if label == "" {
			label = "native"
		}
		b.Run(label, func(b *testing.B) {
			for _, w := range []durationWorkload{durationWorkloads()[0], durationWorkloads()[1], durationWorkloads()[6], durationWorkloads()[7]} {
				b.Run(w.name, func(b *testing.B) {
					p, err := durationEngine(b).Compile(ctx, w.source)
					if err != nil {
						b.Fatal(err)
					}
					options := durationOptions(format)
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
