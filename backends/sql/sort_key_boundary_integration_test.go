//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"os"
	"slices"
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

func TestPostgreSQLSortKeyNumericBoundaries(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const exactLimit int64 = 1 << 52
	const intEndpoint float64 = 1 << 63
	cases := [][]hff.Value{
		{hff.Float(float64(exactLimit)), hff.Int(exactLimit - 1), hff.Float(math.Nextafter(float64(exactLimit), 0)), hff.Int(exactLimit), hff.Float(float64(exactLimit + 1))},
		{hff.Int(-exactLimit), hff.Float(-float64(exactLimit)), hff.Float(math.Nextafter(-float64(exactLimit), 0)), hff.Int(-exactLimit - 1), hff.Float(-float64(exactLimit + 1))},
		{hff.Int(1<<53 + 1), hff.Float(1 << 53), hff.Int(1 << 53), hff.Float(1<<53 + 2)},
		{hff.Float(intEndpoint), hff.Int(math.MaxInt64), hff.Float(math.Nextafter(intEndpoint, 0)), hff.Int(math.MaxInt64 - 1023), hff.Float(math.Nextafter(intEndpoint, math.Inf(1)))},
		{hff.Int(math.MinInt64), hff.Float(-intEndpoint), hff.Float(math.Nextafter(-intEndpoint, 0)), hff.Float(math.Nextafter(-intEndpoint, math.Inf(-1)))},
		{hff.Int(1), hff.Float(math.Nextafter(1, 0)), hff.Float(math.Nextafter(1, 2)), hff.Float(1), hff.Int(-1), hff.Float(math.Nextafter(-1, 0)), hff.Float(-1)},
		{hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0), hff.Float(-math.SmallestNonzeroFloat64), hff.Float(math.SmallestNonzeroFloat64)},
		{hff.Float(math.MaxFloat64), hff.Int(math.MaxInt64), hff.Float(-math.MaxFloat64), hff.Int(math.MinInt64)},
	}
	numbers := schema.Union(schema.IntType, schema.FloatType)
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, typ := range []hff.Type{schema.AnyType, schema.ListOf(numbers)} {
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("data", typ), schema.List("expected", numbers),
			schema.List("kinds", schema.BoolType), schema.List("zeros", schema.BoolType),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `data:sort=expected data:sort:map(v=>v:[int]:exists)=kinds data:sort:map(v=>v=0 v:[float]:exists v:string="-0.0")=zeros`)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...),
				hffsql.BindJSON(hff.Path{"expected"}, hffsql.Column("records", "expected"), hffsql.JSON),
				hffsql.BindJSON(hff.Path{"kinds"}, hffsql.Column("records", "kinds"), hffsql.JSON),
				hffsql.BindJSON(hff.Path{"zeros"}, hffsql.Column("records", "zeros"), hffsql.JSON),
			))
			if err != nil {
				t.Fatal(err)
			}
			var types, args []string
			for _, arg := range q.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, literal)
			}
			for index, values := range cases {
				ordered := slices.Clone(values)
				// Use exact rationals as an independent ordering oracle. Stable
				// ties preserve numeric subtype and the sign of a zero Float.
				slices.SortStableFunc(ordered, func(a, b hff.Value) int {
					return exactSortNumber(a).Cmp(exactSortNumber(b))
				})
				kinds, zeros := make([]hff.Value, len(ordered)), make([]hff.Value, len(ordered))
				for i, value := range ordered {
					_, isInt := value.AsInt()
					v, isFloat := value.AsFloat()
					kinds[i] = hff.Bool(isInt)
					zeros[i] = hff.Bool(isFloat && v == 0 && math.Signbit(v))
				}
				input, want, kindList, zeroList := hff.List(values...), hff.List(ordered...), hff.List(kinds...), hff.List(zeros...)
				row := hff.Object(map[string]hff.Value{"data": input, "expected": want, "kinds": kindList, "zeros": zeroList})
				matched, err := filter.Match(ctx, row)
				if err != nil || !matched {
					t.Fatalf("local exact-order oracle failed case%d: %v %v", index, matched, err)
				}
				name := fmt.Sprintf("boundary%d", len(expected))
				fmt.Fprintf(&script, "PREPARE %s(%s) AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb,%s::json,%s::json,%s::json)) records(data,meta,expected,kinds,zeros); EXECUTE %s(%s); DEALLOCATE %s;\n",
					name, strings.Join(types, ","), q.Where(), sqlFixtureString(metadataJSONFixture(t, input)), format,
					sqlFixtureString(pairTypeMetadata(t, input)), sqlFixtureString(metadataJSONFixture(t, want)),
					sqlFixtureString(metadataJSONFixture(t, kindList)), sqlFixtureString(metadataJSONFixture(t, zeroList)), name, strings.Join(args, ","), name)
				expected = append(expected, true)
				labels = append(labels, fmt.Sprintf("%s/%s case%d", typ.Key(), format, index))
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func exactSortNumber(value hff.Value) *big.Rat {
	if integer, ok := value.AsInt(); ok {
		return new(big.Rat).SetInt64(integer)
	}
	floating, _ := value.AsFloat()
	return new(big.Rat).SetFloat64(floating)
}
