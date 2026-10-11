package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
)

// These benchmarks measure pure translation, not database planning or execution.
func BenchmarkPostgreSQLStructuredBuild(b *testing.B) {
	e := compositeEngine(b)
	ctx := context.Background()
	for _, representation := range []struct {
		name    string
		binding hffsql.Binding
	}{
		{"Composite", hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))},
		{"JSON", hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), hffsql.JSON)},
		{"JSONB", hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), hffsql.JSONB)},
	} {
		b.Run(representation.name, func(b *testing.B) {
			for _, query := range []struct{ name, source string }{
				{"Scalar", `profile.age>=18`},
				{"Any", `profile.people:any(x=>x.name="Alice")`},
				{"Fold", `profile.scores:foldl(0,(acc,x)=>acc+x)>=100`},
			} {
				b.Run(query.name, func(b *testing.B) {
					program, err := e.Compile(ctx, query.source)
					if err != nil {
						b.Fatal(err)
					}
					options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(representation.binding)}
					sample, err := hffsql.Build(ctx, program, options...)
					if err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					for b.Loop() {
						if _, err := hffsql.Build(ctx, program, options...); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportMetric(float64(len(sample.Where())), "sql-bytes")
					b.ReportMetric(float64(len(sample.Args())), "parameters")
				})
			}
		})
	}
}
