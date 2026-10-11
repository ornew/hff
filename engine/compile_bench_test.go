package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

var benchmarkProgram hff.Program

func BenchmarkCompile(b *testing.B) {
	e, err := engine.New(engine.WithSchema(compilerSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct{ name, source string }{
		{"empty", ""},
		{"service_filter", `age>=18 name~"^A" labels.team="platform"`},
		{"typed_union", `x:[list[string]]:contains("api","production")`},
		{"nested_callbacks", `items:any(x=>[x,age]:all(y=>y>10))`},
	}
	for _, count := range []int{10, 100, 1000} {
		terms := make([]string, count)
		for i := range terms {
			terms[i] = fmt.Sprintf("age>%d", i)
		}
		cases = append(cases, struct{ name, source string }{
			fmt.Sprintf("conjunction_%d", count), strings.Join(terms, " "),
		})
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.source)))
			for b.Loop() {
				p, err := e.Compile(ctx, tc.source)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkProgram = p
			}
		})
	}
}
