package syntax

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

var benchmarkTree Expr

// BenchmarkParseQuery measures syntax parsing and its input/tree checks, without
// schema inference, capability checking, or backend construction.
func BenchmarkParseQuery(b *testing.B) {
	cases := []struct{ name, source string }{
		{"empty", ""},
		{"service", `age>=18 name~"^A" labels.team="platform"`},
		{"type_guard", `x:[list[string|null]]:contains("api","production")`},
		{"callbacks", `items:any(x=>x.price>10 x.children:all(y=>y.active))`},
		{"unicode", "`地域`=\"東京\" name=\"\\ud83d\\ude00\""},
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
	for _, depth := range []int{4, 16} {
		cases = append(cases, struct{ name, source string }{
			fmt.Sprintf("parentheses_%d", depth), strings.Repeat("(", depth) + "age>10" + strings.Repeat(")", depth),
		})
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.source)))
			for b.Loop() {
				tree, err := ParseQuery(ctx, tc.source, Config{})
				if err != nil {
					b.Fatal(err)
				}
				benchmarkTree = tree
			}
		})
	}
}
