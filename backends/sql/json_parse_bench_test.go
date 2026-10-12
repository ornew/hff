package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type parsingWorkload struct{ name, source, fixture string }

func parsingWorkloads() []parsingWorkload {
	constant, _ := json.Marshal(parsingRaw("object", 42))
	invalid, _ := json.Marshal(`{"price":42,"bad":{"n":9223372036854775808}}`)
	return []parsingWorkload{
		{"ScalarNumber", `payload:fromjson:type="number"`, "number"},
		{"ScalarString", `payload:fromjson:len>2`, "string"},
		{"ScalarBool", `payload:fromjson=true`, "bool"},
		{"ObjectField", `payload:fromjson.price>10`, "object"},
		{"RepeatedObject", `payload:fromjson.price>10 payload:fromjson.active payload:fromjson.name="Alice"`, "object"},
		{"ArrayPredicate", `payload:fromjson:[list]:any(x=>x>10)`, "array"},
		{"ConstantObject", `payload:fromjson=` + string(constant) + `:fromjson`, "object"},
		{"ConstantInvalidNested", `payload:fromjson=` + string(invalid) + `:fromjson`, "object"},
		{"RepeatedConstants", `payload:fromjson="true":fromjson | payload:fromjson="false":fromjson`, "bool"},
	}
}

func parsingRaw(kind string, i int) string {
	switch kind {
	case "number":
		s := strconv.Itoa(i % 100)
		if i%2 == 0 {
			s += ".0"
		}
		return s
	case "string":
		s := `"Alice"`
		if i%2 == 0 {
			s = `""`
		}
		return s
	case "bool":
		return strconv.FormatBool(i%2 == 0)
	case "array":
		return fmt.Sprintf(`[%d,1,2]`, i%100)
	default:
		return fmt.Sprintf(`{"price":%d,"active":%t,"name":"Alice","unused":[%s]}`, i%100, i%2 == 0, strings.TrimSuffix(strings.Repeat(`{"key":"unused","value":"payload"},`, 8), ","))
	}
}

func parsingFixture(kind string, i int) hff.Value {
	if i%17 == 0 {
		return hff.Null()
	}
	raw := parsingRaw(kind, i)
	switch {
	case i%23 == 0:
		raw = "{"
	case i%29 == 0 && kind == "object":
		raw = strings.TrimSuffix(raw, "}") + `,"bad":{"n":9223372036854775808}}`
	case i%31 == 0 && kind == "object":
		raw = strings.TrimSuffix(raw, "}") + `,"bad":{"x":1,"x":2}}`
	case i%29 == 0 && kind == "array":
		raw = `[1,{"bad":9223372036854775808}]`
	}
	return hff.String(raw)
}

func parsingEngine(tb testing.TB) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.String("payload")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func BenchmarkPostgreSQLJSONParsingBuild(b *testing.B) {
	e := parsingEngine(b)
	ctx := context.Background()
	for _, w := range parsingWorkloads() {
		b.Run(w.name, func(b *testing.B) {
			p, err := e.Compile(ctx, w.source)
			if err != nil {
				b.Fatal(err)
			}
			options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"payload"}, hffsql.Column("records", "payload")))}
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
