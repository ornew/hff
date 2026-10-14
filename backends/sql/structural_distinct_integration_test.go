//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLStructuralDistinctMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	i, f, n := hff.Int, hff.Float, hff.Null()
	obj := func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"n": v}) }
	type fixture struct {
		name        string
		typ         hff.Type
		input, want hff.Value
	}
	seeds := []fixture{
		{"mixed", schema.AnyType, hff.List(i(1), f(1), n, f(math.Copysign(0, -1)), i(0), hff.Bool(false), hff.String("0")), hff.List(i(1), n, f(math.Copysign(0, -1)), hff.Bool(false), hff.String("0"))},
		{"float-first", schema.AnyType, hff.List(f(1), i(1), f(0), i(0)), hff.List(f(1), f(0))},
		{"integer-boundaries", schema.AnyType, hff.List(i(9007199254740993), f(9007199254740992), i(9007199254740992), i(math.MinInt64), f(-0x1p63), i(math.MaxInt64), f(0x1p63)), hff.List(i(9007199254740993), f(9007199254740992), i(math.MinInt64), i(math.MaxInt64), f(0x1p63))},
		{"fractional", schema.AnyType, hff.List(f(0.1), f(math.Nextafter(0.1, 1)), f(0.1), f(math.SmallestNonzeroFloat64), f(math.SmallestNonzeroFloat64)), hff.List(f(0.1), f(math.Nextafter(0.1, 1)), f(math.SmallestNonzeroFloat64))},
		{"containers", schema.AnyType, hff.List(hff.List(), hff.Object(nil), hff.List(n), obj(n), hff.List(), hff.Object(nil), hff.List(n)), hff.List(hff.List(), hff.Object(nil), hff.List(n), obj(n))},
		{"nested-numbers", schema.AnyType, hff.List(hff.List(i(1), n), hff.List(f(1), n), obj(i(1)), obj(f(1)), hff.List(n, i(1))), hff.List(hff.List(i(1), n), obj(i(1)), hff.List(n, i(1)))},
		{"path-boundaries", schema.AnyType, hff.List(
			hff.Object(map[string]hff.Value{"": obj(n), "0": hff.List()}),
			hff.Object(map[string]hff.Value{"0": hff.List(), "": obj(n)}),
			hff.Object(map[string]hff.Value{"a/b": i(1), "a~b": hff.String("あ"), "a\"b": n}),
			hff.Object(map[string]hff.Value{"a": hff.Object(map[string]hff.Value{"b": i(1)})}),
		), hff.List(
			hff.Object(map[string]hff.Value{"": obj(n), "0": hff.List()}),
			hff.Object(map[string]hff.Value{"a/b": i(1), "a~b": hff.String("あ"), "a\"b": n}),
			hff.Object(map[string]hff.Value{"a": hff.Object(map[string]hff.Value{"b": i(1)})}),
		)},
		{"array-object-path", schema.AnyType, hff.List(hff.List(i(1)), hff.Object(map[string]hff.Value{"0": i(1)}), hff.List(i(1))), hff.List(hff.List(i(1)), hff.Object(map[string]hff.Value{"0": i(1)}))},
		{"typed-optional", schema.Struct(schema.Optional(schema.Nullable(schema.Int("n")))), hff.List(hff.Object(nil), obj(n), obj(i(1)), obj(i(1)), hff.Object(nil)), hff.List(hff.Object(nil), obj(n), obj(i(1)))},
		{"typed-numeric-list", schema.ListOf(schema.Union(schema.IntType, schema.FloatType, schema.NullType)), hff.List(hff.List(i(1), n), hff.List(f(1), n), hff.List(n, i(1))), hff.List(hff.List(i(1), n), hff.List(n, i(1)))},
		{"tagged-fallback", schema.Struct(schema.Field("n", schema.BytesType)), hff.List(obj(hff.Bytes([]byte{0, 1})), obj(hff.Bytes([]byte{0, 1})), obj(hff.Bytes([]byte{}))), hff.List(obj(hff.Bytes([]byte{0, 1})), obj(hff.Bytes([]byte{})))},
		{"empty", schema.AnyType, hff.List(), hff.List()},
	}
	var cases []fixture
	for _, seed := range seeds {
		cases = append(cases, seed)
		if seed.typ.Kind() != hff.AnyType {
			continue
		}
		for depth := 1; depth <= 3; depth++ {
			wrap := func(list hff.Value) hff.Value {
				values, _ := list.AsList()
				result := make([]hff.Value, len(values))
				for index, value := range values {
					for level := 0; level < depth; level++ {
						value = hff.Object(map[string]hff.Value{"": hff.List(value), "[]": n})
					}
					result[index] = value
				}
				return hff.List(result...)
			}
			cases = append(cases, fixture{fmt.Sprintf("%s/depth%d", seed.name, depth), schema.AnyType, wrap(seed.input), wrap(seed.want)})
		}
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, tc := range cases {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", tc.typ), schema.List("expected", tc.typ), schema.List("kinds", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(tc.name, err)
		}
		want, _ := tc.want.AsList()
		kinds := make([]hff.Value, len(want))
		for index, value := range want {
			kind := value.Kind().String()
			if value.Kind() == hff.IntKind || value.Kind() == hff.FloatKind {
				kind = "number"
			}
			kinds[index] = hff.String(kind)
		}
		record := hff.Object(map[string]hff.Value{"items": tc.input, "expected": tc.want, "kinds": hff.List(kinds...)})
		sources := []string{`items:distinct=expected`, fmt.Sprintf(`items:distinct:size=%d`, len(want)), `items:distinct:map(x=>x:type)=kinds`}
		switch tc.name {
		case "mixed":
			sources = append(sources, `items:distinct[0]:[int]:exists items:distinct[2]:[float]:exists items:distinct[2]:json="-0.0"`)
		case "float-first":
			sources = append(sources, `items:distinct[0]:[float]:exists items:distinct[1]:[float]:exists`)
		case "nested-numbers", "typed-numeric-list":
			sources = append(sources, `items:distinct[0][0]:[int]:exists items:distinct[1].n:[int]:exists`)
			if tc.name == "typed-numeric-list" {
				sources[len(sources)-1] = `items:distinct[0][0]:[int]:exists items:distinct[1][1]:[int]:exists`
			}
		}
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := filter.Match(ctx, record); err != nil || !got {
				t.Fatalf("bad fixture %s/%s: %v %v", tc.name, source, got, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...),
					hffsql.BindJSON(hff.Path{"expected"}, hffsql.Column("records", "expected"), hffsql.JSON),
					hffsql.BindJSON(hff.Path{"kinds"}, hffsql.Column("records", "kinds"), hffsql.JSON),
				))
				if err != nil {
					t.Fatal(tc.name, source, err)
				}
				from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb,%s::json,%s::json)) records(items,metadata,expected,kinds)", sqlFixtureString(metadataJSONFixture(t, tc.input)), format, sqlFixtureString(pairTypeMetadata(t, tc.input)), sqlFixtureString(metadataJSONFixture(t, tc.want)), sqlFixtureString(metadataJSONFixture(t, hff.List(kinds...))))
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, true)
				labels = append(labels, tc.name+"/"+string(format)+"/"+source)
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("verified %d stable structural distinct results", len(expected))
}

func TestPostgreSQLStructuralDistinctRejectedFloatLeaves(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, tc := range []struct {
		name        string
		element     hff.Type
		raw, source string
	}{
		{"any-underflow", schema.AnyType, `[[1e-400],[1e-400],[null],[null]]`, `items:distinct:size=3`},
		{"any-overflow", schema.AnyType, `[{"n":1e400},{"n":1e400},{"n":null},{"n":null}]`, `items:distinct:size=3`},
		{"typed-float-null", schema.ListOf(schema.NullableType(schema.FloatType)), `[[1e-400],[1e-400],[null],[null]]`, `items:distinct:size=1`},
		{"alternate-spellings", schema.AnyType, `[[0.1],[0.10000000000000001],[1.0],[1],[1.00000000000000001]]`, `items:distinct:size=2`},
		{"object-order", schema.AnyType, `[{"a":1,"b":null},{"b":null,"a":1.0}]`, `items:distinct:size=1`},
		{"signed-zero-first", schema.AnyType, `[[-0.0],[0.0],[0]]`, `items:distinct[0][0]:json="-0.0" items:distinct:size=1`},
		{"float-subtype-first", schema.AnyType, `[[1.0],[1]]`, `items:distinct[0][0]:[float]:exists items:distinct:size=1`},
		{"integer-subtype-first", schema.AnyType, `[[1],[1.0]]`, `items:distinct[0][0]:[int]:exists items:distinct:size=1`},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", tc.element))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
		if err != nil {
			t.Fatal(tc.name, err)
		}
		appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(tc.raw)+"::json)) records(items)", len(expected))
		expected = append(expected, true)
		labels = append(labels, tc.name)
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
