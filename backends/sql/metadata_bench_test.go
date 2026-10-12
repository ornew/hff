package sql_test

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

var metadataBenchmarkQueries = []struct{ name, source string }{
	{"Scalar", `doc.price>10.5`},
	{"Repeated", `doc.price>10.5 doc.price<90 doc.price!=25 doc.price=doc.price:[float]`},
	{"Guards", `doc.price:[float]>10.5 doc.items:[list[float]]:size>=4`},
	{"Callback", `doc.items:[list[float]]:any(x=>x>3) doc.price>10.5`},
}

func metadataBenchmarkEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Map("doc", schema.AnyType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func metadataBenchmarkBinding(format hffsql.JSONFormat) hffsql.Binding {
	if format == hffsql.JSONB {
		return hffsql.BindJSON(hff.Path{"doc"}, hffsql.Column("records", "doc"), format, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	}
	return hffsql.BindJSON(hff.Path{"doc"}, hffsql.Column("records", "doc"), format)
}

// Measure pure Build costs separately from PostgreSQL planning and execution.
func BenchmarkPostgreSQLMetadataBuild(b *testing.B) {
	e := metadataBenchmarkEngine(b)
	ctx := context.Background()
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		name := "NativeJSON"
		if format == hffsql.JSONB {
			name = "JSONBMetadata"
		}
		b.Run(name, func(b *testing.B) {
			for _, query := range metadataBenchmarkQueries {
				b.Run(query.name, func(b *testing.B) {
					program, err := e.Compile(ctx, query.source)
					if err != nil {
						b.Fatal(err)
					}
					options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(metadataBenchmarkBinding(format))}
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

// Both storage modes describe the same typed records. Unused payload children
// make whole-document reconstruction visible without changing filter results.
func metadataBenchmarkFixture(i int) (hff.Value, string, string) {
	if i%17 == 0 {
		return hff.Null(), "null", `{"version":1,"types":{}}`
	}
	price := float64(i % 100)
	if i%19 == 0 {
		price = math.Copysign(0, -1)
	}
	floatText := func(x float64) string {
		text := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return text
	}
	priceText := floatText(price)
	types := map[string]map[string]string{"/price": {"type": "float", "text": priceText}}
	var items []hff.Value
	for index := 0; index < 5; index++ {
		items = append(items, hff.Float(float64(index)))
		types["/items/"+strconv.Itoa(index)] = map[string]string{"type": "float", "text": floatText(float64(index))}
	}
	var payload []hff.Value
	for index := 0; index < 8; index++ {
		payload = append(payload, hff.Object(map[string]hff.Value{"key": hff.String("unused"), "value": hff.String("payload")}))
	}
	value := hff.Object(map[string]hff.Value{"price": hff.Float(price), "items": hff.List(items...), "payload": hff.List(payload...), "name": hff.String("Alice")})
	rawPayload := strings.TrimSuffix(strings.Repeat(`{"key":"unused","value":"payload"},`, 8), ",")
	raw := `{"price":` + priceText + `,"items":[0.0,1.0,2.0,3.0,4.0],"payload":[` + rawPayload + `],"name":"Alice"}`
	metadata, _ := json.Marshal(map[string]any{"version": 1, "types": types})
	return value, raw, string(metadata)
}
