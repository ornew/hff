package local

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff/internal/core"
)

var jsonBenchmarkValue core.Value

func BenchmarkJSONCodec(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		var text strings.Builder
		text.WriteByte('[')
		for i := range count {
			if i > 0 {
				text.WriteByte(',')
			}
			text.WriteString(`{"name":"service-` + strconv.Itoa(i) + `","score":1.0,"tags":["api","production"],"region":"東京"}`)
		}
		text.WriteByte(']')
		source := text.String()
		setup := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		decoded, err := setup.decodeJSON(source, 64)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(strconv.Itoa(count)+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
				value, err := e.decodeJSON(source, 64)
				if err != nil {
					b.Fatal(err)
				}
				jsonBenchmarkValue = value.value
			}
		})
		b.Run(strconv.Itoa(count)+"/encode", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
				value, err := e.encodeJSON(decoded.value, 64)
				if err != nil {
					b.Fatal(err)
				}
				jsonBenchmarkValue = value.value
			}
		})
	}
}
