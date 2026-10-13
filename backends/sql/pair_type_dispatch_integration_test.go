//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
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

func TestPostgreSQLPairTypeDispatchMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type pair [2]hff.Value
	stringBool := schema.Union(schema.StringType, schema.BoolType, schema.NullType)
	stringInt := schema.Union(schema.StringType, schema.IntType, schema.NullType)
	stringFloat := schema.Union(schema.StringType, schema.FloatType, schema.NullType)
	nullableInt := schema.NullableType(schema.IntType)
	nullableFloat := schema.NullableType(schema.FloatType)
	obj := func(values map[string]hff.Value) hff.Value { return hff.Object(values) }
	for _, profile := range []struct {
		name        string
		left, right hff.Type
		pairs       []pair
	}{
		{"string_bool", stringBool, stringBool, []pair{
			{hff.Null(), hff.Null()}, {hff.String("a"), hff.String("a")}, {hff.String("é"), hff.String("z")},
			{hff.Bool(true), hff.Bool(false)}, {hff.Bool(false), hff.Bool(false)},
			{hff.String("true"), hff.Bool(true)}, {hff.Null(), hff.Bool(false)},
		}},
		{"string_int", stringInt, stringInt, []pair{
			{hff.Null(), hff.Int(0)}, {hff.String("1"), hff.Int(1)}, {hff.Int(1), hff.Int(1)},
			{hff.Int(math.MinInt64), hff.Int(math.MaxInt64)},
			{hff.Int(9007199254740993), hff.Int(9007199254740992)},
			{hff.String("a"), hff.String("b")},
		}},
		{"string_float", stringFloat, stringFloat, []pair{
			{hff.Null(), hff.Float(0)}, {hff.String("1"), hff.Float(1)},
			{hff.Float(1), hff.Float(1)}, {hff.Float(math.Copysign(0, -1)), hff.Float(0)},
			{hff.Float(math.SmallestNonzeroFloat64), hff.Float(0)},
			{hff.Float(math.MaxFloat64), hff.Float(math.Nextafter(math.MaxFloat64, 0))},
			{hff.String("é"), hff.String("z")},
		}},
		{"asymmetric_numeric", stringInt, stringFloat, []pair{
			{hff.Null(), hff.Null()}, {hff.Int(1), hff.Float(1)},
			{hff.Int(9007199254740993), hff.Float(9007199254740992)},
			{hff.Int(math.MaxInt64), hff.Float(9223372036854775808)},
			{hff.Int(math.MinInt64), hff.Float(-9223372036854775808)},
			{hff.Int(0), hff.Float(math.SmallestNonzeroFloat64)},
			{hff.String("1"), hff.Float(1)}, {hff.String("é"), hff.String("z")},
		}},
		{"disjoint_containers", schema.Union(schema.StringType, schema.ListOf(nullableInt), schema.NullType), schema.Union(schema.StringType, schema.MapOf(nullableInt), schema.NullType), []pair{
			{hff.Null(), hff.Null()}, {hff.List(), obj(nil)},
			{hff.List(hff.Int(1)), obj(map[string]hff.Value{"n": hff.Int(1)})},
			{hff.List(hff.Null()), hff.String("a")}, {hff.String("a"), obj(nil)},
			{hff.String("a"), hff.String("a")},
		}},
		{"map_struct", schema.Union(schema.StringType, schema.MapOf(nullableInt), schema.NullType), schema.Union(schema.StringType, schema.Struct(schema.Optional(schema.Nullable(schema.Float("n")))), schema.NullType), []pair{
			{hff.Null(), hff.Null()}, {obj(nil), obj(nil)},
			{obj(map[string]hff.Value{"n": hff.Int(1)}), obj(map[string]hff.Value{"n": hff.Float(1)})},
			{obj(map[string]hff.Value{"n": hff.Int(9007199254740993)}), obj(map[string]hff.Value{"n": hff.Float(9007199254740992)})},
			{obj(map[string]hff.Value{"n": hff.Null()}), obj(nil)},
			{obj(map[string]hff.Value{"n": hff.Null()}), obj(map[string]hff.Value{"n": hff.Null()})},
			{obj(map[string]hff.Value{"extra": hff.Int(1)}), obj(nil)},
			{hff.String("a"), hff.String("b")},
		}},
		{"array_union", schema.Union(schema.StringType, schema.ListOf(nullableInt), schema.NullType), schema.Union(schema.StringType, schema.ListOf(nullableFloat), schema.NullType), []pair{
			{hff.Null(), hff.Null()}, {hff.List(), hff.List()},
			{hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null())},
			{hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Float(2), hff.Float(1))},
			{hff.List(), hff.List(hff.Null())},
			{hff.List(hff.Int(9007199254740993)), hff.List(hff.Float(9007199254740992))},
			{hff.String("a"), hff.List()}, {hff.String("a"), hff.String("a")},
		}},
		{"nullable_map_fields", schema.NullableType(schema.MapOf(schema.AnyType)), schema.NullableType(schema.MapOf(schema.AnyType)), []pair{
			{hff.Null(), hff.Null()}, {obj(nil), obj(nil)},
			{obj(map[string]hff.Value{"a": hff.String("a")}), obj(map[string]hff.Value{"a": hff.String("a")})},
			{obj(map[string]hff.Value{"a": hff.Null()}), obj(nil)},
			{obj(map[string]hff.Value{"a": hff.Bool(false)}), obj(map[string]hff.Value{"a": hff.Int(0)})},
			{obj(map[string]hff.Value{"a": hff.Int(9007199254740993)}), obj(map[string]hff.Value{"a": hff.Float(9007199254740992)})},
			{obj(map[string]hff.Value{"a": hff.List(hff.Int(1), hff.Null()), "extra": hff.Float(1)}), obj(map[string]hff.Value{"a": hff.List(hff.Float(1), hff.Null())})},
		}},
		{"any", schema.AnyType, schema.AnyType, []pair{
			{hff.Null(), hff.Null()}, {hff.Bool(false), hff.Bool(false)},
			{hff.List(hff.Int(1), hff.String("a")), hff.Float(1)},
			{hff.List(hff.Int(9007199254740993), hff.String("a")), hff.Float(9007199254740992)},
			{hff.List(hff.String("a"), hff.Int(1)), hff.Int(1)},
			{hff.Int(9007199254740993), hff.Float(9007199254740992)},
			{hff.Float(math.Copysign(0, -1)), hff.Int(0)},
			{hff.String("é"), hff.String("z")}, {hff.String("1"), hff.Int(1)},
			{hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null())},
			{obj(map[string]hff.Value{"n": hff.Int(1)}), obj(map[string]hff.Value{"n": hff.Float(1)})},
		}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", profile.left), schema.Field("right", profile.right))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			sources := []string{
				`left=right`, `left!=right`, `right=left`, `left<right`, `right<left`,
				`!(left=right)`, `(left=right):type="null"`, `(left<right):type="null"`,
				`(left=right):exists`, `(left!<right):type="bool"`,
				`left:[string|int]=right`, `(left:[string|int]=right):exists`,
			}
			if profile.name == "nullable_map_fields" {
				for i := range sources {
					sources[i] = strings.ReplaceAll(strings.ReplaceAll(sources[i], "left", "left.a"), "right", "right.a")
				}
			}
			if profile.name == "any" {
				sources = append(sources,
					`left:[list[any]]:any(x=>x=right)`,
					`left:[list[any]]:any(x=>left:[list[any]]:any(x=>x=right))`,
					`left:[list[any]]:foldl(right,(acc,x)=>acc)=right`,
					`left:[list[any]]:foldr(right,(x,acc)=>acc)=right`,
					`left:[list[int|string]]:any(x=>x=right)`,
					`left:[list[int|string]]:foldl("seed",(acc,x)=>x)=right`,
				)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var script strings.Builder
				var expected []bool
				var labels []string
				for si, source := range sources {
					program, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, program)
					if err != nil {
						t.Fatal(err)
					}
					query := pairDispatchQuery(t, profile.left, profile.right, source, format, format == hffsql.JSONB)
					var types, args []string
					for _, arg := range query.Args() {
						typ, literal := sqlFixtureParameter(t, arg)
						types, args = append(types, typ), append(args, literal)
					}
					var rows []string
					for ri, pair := range profile.pairs {
						want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": pair[0], "right": pair[1]}))
						if err != nil {
							t.Fatal(source, err)
						}
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s %s row%d", source, format, ri))
						leftSQL := sqlFixtureString(metadataJSONFixture(t, pair[0])) + "::" + string(format)
						rightSQL := sqlFixtureString(metadataJSONFixture(t, pair[1])) + "::" + string(format)
						metadataSQL := sqlFixtureString(pairTypeMetadata(t, pair[0])) + "::jsonb," + sqlFixtureString(pairTypeMetadata(t, pair[1])) + "::jsonb"
						rows = append(rows, "("+leftSQL+","+rightSQL+","+metadataSQL+")")
						if pair[0].Kind() == hff.NullKind || pair[1].Kind() == hff.NullKind {
							// Native SQL NULL and JSON null represent the same HFF
							// null, including guards and observed comparison types.
							if pair[0].Kind() == hff.NullKind {
								leftSQL = "NULL::" + string(format)
							}
							if pair[1].Kind() == hff.NullKind {
								rightSQL = "NULL::" + string(format)
							}
							rows = append(rows, "("+leftSQL+","+rightSQL+","+metadataSQL+")")
							expected = append(expected, want)
							labels = append(labels, fmt.Sprintf("%s %s SQL-null row%d", source, format, ri))
						}
					}
					parameters, arguments := "", ""
					if len(types) > 0 {
						parameters = "(" + strings.Join(types, ",") + ")"
						arguments = "(" + strings.Join(args, ",") + ")"
					}
					fmt.Fprintf(&script, "PREPARE pair_dispatch_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(\"left\",\"right\",left_meta,right_meta); EXECUTE pair_dispatch_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
				}
				cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				cmd.Stdin = strings.NewReader(script.String())
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				results := strings.Fields(string(out))
				if len(results) != len(expected) {
					t.Fatalf("got%d results,want%d:%s", len(results), len(expected), out)
				}
				t.Logf("checked %d query/record results for %s", len(results), format)
				for i, result := range results {
					if result == "n" || (result == "true") != expected[i] {
						t.Errorf("%s:SQL=%s local=%v", labels[i], result, expected[i])
					}
				}
			}
		})
	}
}
