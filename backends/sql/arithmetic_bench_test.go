package sql_test

import (
	"context"
	"math"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

var arithmeticWorkloads = []struct{ name, source string }{
	{"CompareControl", `left>right`},
	{"Add", `left+right>0`},
	{"Subtract", `left-right>0`},
	{"Multiply", `left*right>0`},
	{"Divide", `left/right>0`},
	{"Repeated", `left+right>0 left-right<100 left*right!=0`},
	{"Guarded", `left!=null right!=null left+right>0`},
	{"Constant", `left+0.1>0`},
}

func arithmeticEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Float("left")), schema.Nullable(schema.Float("right")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func arithmeticOptions() []hffsql.Option {
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.Bind(hff.Path{"left"}, hffsql.Column("records", "left")),
		hffsql.Bind(hff.Path{"right"}, hffsql.Column("records", "right")),
	)}
}

func arithmeticFixture(i int) (hff.Value, hff.Value) {
	pairs := [][2]float64{{0, 0}, {math.Copysign(0, -1), 1}, {0.1, 0.2}, {1, 3}, {-1, 3}, {math.SmallestNonzeroFloat64, 2}, {3 * math.SmallestNonzeroFloat64, 2}, {math.MaxFloat64, 2}, {math.MaxFloat64, math.MaxFloat64}, {1e-308, 1e308}, {9007199254740992, 1}, {5, 2}}
	pair := pairs[i%len(pairs)]
	l, r := hff.Float(pair[0]), hff.Float(pair[1])
	if i%17 == 0 {
		l = hff.Null()
	}
	if i%19 == 0 {
		r = hff.Null()
	}
	return l, r
}

func BenchmarkPostgreSQLArithmeticBuild(b *testing.B) {
	ctx := context.Background()
	e := arithmeticEngine(b)
	for _, w := range arithmeticWorkloads {
		b.Run(w.name, func(b *testing.B) {
			p, err := e.Compile(ctx, w.source)
			if err != nil {
				b.Fatal(err)
			}
			options := arithmeticOptions()
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
