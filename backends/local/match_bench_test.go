package local_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// BenchmarkRecordMatching compares the same completed match with and without
// per-call input validation. Construction, compilation, and Build are outside
// the timed loops; MatchInput also reuses validation performed during setup.
func BenchmarkRecordMatching(b *testing.B) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.String("name"), schema.Int("age"),
		schema.Map("labels", schema.StringType),
		schema.List("items", schema.Struct(schema.Int("price"), schema.Bool("active"))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{1, 100, 1000} {
		items := make([]hff.Value, count)
		for i := range items {
			items[i] = hff.Object(map[string]hff.Value{
				"price": hff.Int(int64(i)), "active": hff.Bool(true),
			})
		}
		record := hff.Object(map[string]hff.Value{
			"name": hff.String("Alice"), "age": hff.Int(20),
			"labels": hff.Object(map[string]hff.Value{"team": hff.String("platform")}),
			"items":  hff.List(items...),
		})
		input, err := e.Validate(ctx, record)
		if err != nil {
			b.Fatal(err)
		}
		cases := []struct{ name, source string }{
			{"scalar", `age>=18 name="Alice"`},
			{"service", `age>=18 name~"^A" labels.team="platform"`},
			{"any_first", `items:any(x=>x.active)`},
			{"any_last", fmt.Sprintf(`items:any(x=>x.price=%d)`, count-1)},
			{"all", `items:all(x=>x.active)`},
		}
		for _, tc := range cases {
			program, err := e.Compile(ctx, tc.source)
			if err != nil {
				b.Fatal(err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				b.Fatal(err)
			}
			for _, reuse := range []bool{false, true} {
				mode := "Match"
				if reuse {
					mode = "MatchInput"
				}
				b.Run(fmt.Sprintf("%s/%d/%s", tc.name, count, mode), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						var matched bool
						var err error
						if reuse {
							matched, err = filter.MatchInput(ctx, input)
						} else {
							matched, err = filter.Match(ctx, record)
						}
						if err != nil || !matched {
							b.Fatalf("expected a completed match, got %v (%v)", matched, err)
						}
					}
				})
			}
		}
	}
}
