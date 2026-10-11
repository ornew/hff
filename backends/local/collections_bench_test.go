package local_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

var collectionBenchmarkMatch bool

func BenchmarkCollections(b *testing.B) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.List("items", schema.IntType), schema.List("sorted", schema.IntType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{1, 100, 1000} {
		ascending, descending := make([]hff.Value, count), make([]hff.Value, count)
		for i := range ascending {
			ascending[i] = hff.Int(int64(i))
			descending[i] = hff.Int(int64(count - i - 1))
		}
		input, err := e.Validate(ctx, hff.Object(map[string]hff.Value{
			"items": hff.List(ascending...), "sorted": hff.List(ascending...),
		}))
		if err != nil {
			b.Fatal(err)
		}
		sortInput, err := e.Validate(ctx, hff.Object(map[string]hff.Value{
			"items": hff.List(descending...), "sorted": hff.List(ascending...),
		}))
		if err != nil {
			b.Fatal(err)
		}
		cases := []struct {
			name, source string
			match        bool
			abort        bool
			sort         bool
		}{
			{name: "any_first", source: `items:any(x=>x>=0)`, match: true},
			{name: "any_last", source: fmt.Sprintf(`items:any(x=>x=%d)`, count-1), match: true},
			{name: "any_miss", source: `items:any(x=>x<0)`},
			{name: "all", source: `items:all(x=>x>=0)`, match: true},
			{name: "map", source: fmt.Sprintf(`items:map(x=>x+1):size=%d`, count), match: true},
			{name: "fold", source: fmt.Sprintf(`items:foldl(0,(acc,x)=>acc+x)=%d`, count*(count-1)/2), match: true},
			{name: "sort", source: `items:sort=sorted`, match: true, sort: true},
			// This quadratic search intentionally exercises the finite default
			// evaluator budget at 1000 elements, rather than disabling limits.
			{name: "nested_search", source: `items:all(x=>items:any(y=>y=x))`, match: true, abort: count == 1000},
		}
		for _, tc := range cases {
			b.Run(fmt.Sprintf("%s/%d", tc.name, count), func(b *testing.B) {
				p, err := e.Compile(ctx, tc.source)
				if err != nil {
					b.Fatal(err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					b.Fatal(err)
				}
				data := input
				if tc.sort {
					data = sortInput
				}
				b.ReportAllocs()
				for b.Loop() {
					matched, err := filter.MatchInput(ctx, data)
					if tc.abort {
						var detail *hff.Error
						if matched || !errors.As(err, &detail) || detail.Code != hff.LimitError {
							b.Fatalf("expected a budget abort, got %v (%v)", matched, err)
						}
					} else if err != nil || matched != tc.match {
						b.Fatalf("expected %v, got %v (%v)", tc.match, matched, err)
					}
					collectionBenchmarkMatch = matched
				}
			})
		}
	}
}
