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

func TestNormalizedMapOutputAvoidsRepeatedTraversal(t *testing.T) {
	for _, tc := range []struct {
		name       string
		typ        hff.Type
		source     string
		traversals int
	}{
		{"dynamic serialization", schema.Union(schema.IntType, schema.FloatType), `items:map(x=>x):json="[]"`, 2},
		{"nested maps", schema.FloatType, `[items:map(x=>items:map(y=>y))]:size>0`, 3},
		{"container callback", schema.ListOf(schema.FloatType), `[items:map(x=>x)]:size>0`, 2},
		{"bytes callback", schema.BytesType, `[items:map(x=>x)]:size>0`, 1},
		{"struct callback", schema.Struct(schema.Int("i"), schema.Float("f")), `[items:map(x=>x)]:size>0`, 1},
		// Controls retain their existing normalization without a map-output proof.
		{"guard control", schema.Union(schema.IntType, schema.FloatType), `(items:map(x=>x))!:json="[]"`, 3},
		{"flatmap control", schema.FloatType, `[items:flatmap(x=>[x])]:size>0`, 4},
		{"fold control", schema.FloatType, `[items:foldl([], (a,x)=>a:concat([x]))]:size>0`, 8},
		{"literal control", schema.FloatType, `[items]:size>0`, 1},
		{"size control", schema.FloatType, `items:map(x=>x):size>0`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("items", schema.NullableType(schema.ListOf(schema.NullableType(tc.typ)))))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(context.Background(), tc.source)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(q.Where(), "json_array_elements("); got != tc.traversals {
				t.Errorf("generated traversal sites=%d, want %d", got, tc.traversals)
			}
		})
	}
}

func TestNormalizedMapOutputRetainsFloatLossChecks(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{`floats:map(x=>x):json="[]"`, `[floats:map(x=>x)]:json="[]"`, `floats:map(x=>x):map(y=>y):json="[]"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSONB, false)...)
		requireCode(t, err, hff.BackendError)
		if _, err = hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSONB, true)...); err != nil {
			t.Fatal(source, err)
		}
	}
}
