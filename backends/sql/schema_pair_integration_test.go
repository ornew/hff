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

func TestPostgreSQLSchemaPairMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type pair [2]hff.Value
	obj := func(values map[string]hff.Value) hff.Value { return hff.Object(values) }
	str := schema.StringType
	list := schema.Union(str, schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType), schema.NullType)
	fields := schema.Union(str,
		schema.Struct(schema.Int("i"), schema.Optional(schema.Nullable(schema.Float("f")))),
		schema.Struct(schema.Int("i"), schema.Optional(schema.String("label"))), schema.NullType)
	mapped := schema.Union(str, schema.MapOf(schema.IntType), schema.Struct(schema.Float("")), schema.NullType)
	mappedRight := schema.Union(str, schema.MapOf(schema.FloatType), schema.Struct(schema.Int("fixed")), schema.NullType)
	numbers := schema.Union(schema.IntType, schema.FloatType, schema.NullType)
	mixedRoot := schema.Union(str, schema.BoolType, numbers, schema.ListOf(numbers), schema.MapOf(numbers))
	payload := schema.Union(str, schema.Struct(schema.Any("payload"), schema.Int("id")), schema.NullType)
	nested := schema.Union(str, schema.ListOf(schema.ListOf(schema.IntType)), schema.NullType)
	for _, profile := range []struct {
		name        string
		left, right hff.Type
		metadata    bool
		pairs       []pair
		extra       []string
	}{
		{"scalar_and_container_families", mixedRoot, mixedRoot, true, []pair{
			{hff.Int(1), hff.Float(1)}, {hff.Int(9007199254740993), hff.Float(9007199254740992)},
			{hff.Float(math.Copysign(0, -1)), hff.Int(0)}, {hff.String("1"), hff.Float(1)},
			{hff.Bool(false), hff.Bool(false)}, {hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null())},
			{hff.List(hff.Int(1)), obj(map[string]hff.Value{"0": hff.Int(1)})},
			{obj(nil), obj(map[string]hff.Value{"a": hff.Null()})},
			{hff.Null(), hff.Null()},
		}, nil},
		{"overlap_lists", list, list, true, []pair{
			{hff.Null(), hff.Null()}, {hff.String("a"), hff.String("a")}, {hff.List(), hff.List()},
			{hff.List(hff.Int(1)), hff.List(hff.Float(1))},
			{hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Float(2), hff.Float(1))},
			{hff.List(hff.Int(9007199254740993)), hff.List(hff.Float(9007199254740992))},
			{hff.List(hff.Int(math.MaxInt64)), hff.List(hff.Float(9223372036854775808))},
			{hff.List(hff.Float(math.Copysign(0, -1))), hff.List(hff.Float(0))},
		}, []string{`left:[list[int]]=right`, `left:[list[float]]=right:[list[number]]`}},
		{"distinct_field_types", fields, fields, false, []pair{
			{hff.Null(), obj(map[string]hff.Value{"i": hff.Int(1)})},
			{obj(map[string]hff.Value{"i": hff.Int(1)}), obj(map[string]hff.Value{"i": hff.Int(1)})},
			{obj(map[string]hff.Value{"i": hff.Int(1)}), obj(map[string]hff.Value{"i": hff.Int(1), "f": hff.Null()})},
			{obj(map[string]hff.Value{"i": hff.Int(1), "f": hff.Float(1e20)}), obj(map[string]hff.Value{"f": hff.Float(1e20), "i": hff.Int(1)})},
			{obj(map[string]hff.Value{"i": hff.Int(1), "f": hff.Float(1)}), obj(map[string]hff.Value{"i": hff.Int(1), "label": hff.String("1")})},
		}, nil},
		{"map_struct_empty_key", mapped, mappedRight, true, []pair{
			{obj(nil), obj(nil)}, {obj(map[string]hff.Value{"": hff.Float(1)}), obj(map[string]hff.Value{"": hff.Float(1)})},
			{obj(map[string]hff.Value{"tail": hff.Int(1)}), obj(map[string]hff.Value{"tail": hff.Float(1)})},
			{obj(map[string]hff.Value{"a/b~\n": hff.Int(9007199254740993)}), obj(map[string]hff.Value{"a/b~\n": hff.Float(9007199254740992)})},
			{obj(map[string]hff.Value{"fixed": hff.Int(1)}), obj(map[string]hff.Value{"fixed": hff.Int(1)})},
		}, []string{`left:[map[number]]=right`}},
		{"nested_any", payload, payload, true, []pair{
			{obj(map[string]hff.Value{"payload": hff.Null(), "id": hff.Int(1)}), obj(map[string]hff.Value{"payload": hff.Null(), "id": hff.Int(1)})},
			{obj(map[string]hff.Value{"payload": hff.List(hff.Int(1), hff.Null()), "id": hff.Int(1)}), obj(map[string]hff.Value{"payload": hff.List(hff.Float(1), hff.Null()), "id": hff.Int(1)})},
			{obj(map[string]hff.Value{"payload": hff.List(hff.Int(9007199254740993)), "id": hff.Int(1)}), obj(map[string]hff.Value{"payload": hff.List(hff.Float(9007199254740992)), "id": hff.Int(1)})},
			{obj(map[string]hff.Value{"payload": obj(nil), "id": hff.Int(1)}), obj(map[string]hff.Value{"payload": obj(map[string]hff.Value{"missing": hff.Null()}), "id": hff.Int(1)})},
		}, nil},
		{"never_containers", schema.Union(str, schema.ListOf(schema.NeverType), schema.Struct()), schema.Union(str, schema.ListOf(schema.IntType), schema.MapOf(schema.IntType)), false, []pair{
			{hff.List(), hff.List()}, {hff.List(), hff.List(hff.Int(1))},
			{obj(nil), obj(nil)}, {obj(nil), obj(map[string]hff.Value{"a": hff.Int(1)})},
			{hff.List(), obj(nil)}, {obj(nil), hff.List()},
		}, nil},
		{"callback_scope", nested, schema.Union(str, schema.ListOf(schema.IntType), schema.NullType), true, []pair{
			{hff.List(hff.List(hff.Int(1)), hff.List(hff.Int(2))), hff.List(hff.Int(1))},
			{hff.List(hff.List(hff.Int(1)), hff.List(hff.Int(2))), hff.List(hff.Int(2))},
			{hff.List(), hff.List()}, {hff.Null(), hff.List()},
		}, []string{`left:any(x=>x=right)`, `left:any(x=>left:any(x=>x=right))`, `left:foldl(right,(acc,x)=>x)=right`, `left:foldr(right,(x,acc)=>x)=right`, `left:any(x=>x:[list[int]]=right:[list[int]])`}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", profile.left), schema.Field("right", profile.right))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			sources := append([]string{`left=right`, `left!=right`, `right=left`, `!(left=right)`, `(left=right):type="null"`, `(left=right):exists`, `left!=right!`}, profile.extra...)
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var script strings.Builder
				var expected []bool
				var labels []string
				for si, source := range sources {
					p, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					f, err := local.Build(ctx, p)
					if err != nil {
						t.Fatal(source, err)
					}
					q := pairDispatchQuery(t, profile.left, profile.right, source, format, format == hffsql.JSONB && profile.metadata)
					var types, args, rows []string
					for _, arg := range q.Args() {
						typ, literal := sqlFixtureParameter(t, arg)
						types, args = append(types, typ), append(args, literal)
					}
					for pi, pair := range profile.pairs {
						want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"left": pair[0], "right": pair[1]}))
						if err != nil {
							t.Fatal(source, pi, err)
						}
						rows = append(rows, "("+sqlFixtureString(metadataJSONFixture(t, pair[0]))+"::"+string(format)+","+sqlFixtureString(metadataJSONFixture(t, pair[1]))+"::"+string(format)+","+sqlFixtureString(pairTypeMetadata(t, pair[0]))+"::jsonb,"+sqlFixtureString(pairTypeMetadata(t, pair[1]))+"::jsonb)")
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s %s pair%d", source, format, pi))
					}
					parameters, arguments := "", ""
					if len(types) > 0 {
						parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
					}
					fmt.Fprintf(&script, "PREPARE schema_%d%s AS SELECT %s FROM (VALUES %s) records(\"left\",\"right\",left_meta,right_meta); EXECUTE schema_%d%s;\n", si, parameters, q.Where(), strings.Join(rows, ","), si, arguments)
				}
				cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				cmd.Stdin = strings.NewReader(script.String())
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				results := strings.Fields(string(out))
				if len(results) != len(expected) {
					t.Fatalf("got %d results,want %d: %s", len(results), len(expected), out)
				}
				for i, result := range results {
					if (result == "t") != expected[i] {
						t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
					}
				}
				t.Logf("checked %d schema-pair comparisons in %s", len(expected), format)
			}
		})
	}
}

func TestPostgreSQLSchemaPairFloatFailureIsNestedNull(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	typ := schema.Union(schema.StringType, schema.ListOf(schema.NullableType(schema.FloatType)))
	var script strings.Builder
	var expected []bool
	for i, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q := pairDispatchQuery(t, typ, typ, `left=right`, format, false)
		if len(q.Args()) != 0 {
			t.Fatal("unexpected Float-list parameters")
		}
		// Native Float input rejects tiny spellings. The original document
		// normalizer turned their values into null before nested comparison.
		// Preserve that result in the bounded path, including nested nulls.
		var rows []string
		for _, pair := range [][2]string{
			{`[1e-400]`, `[null]`}, {`[-1e-400]`, `[1e-400]`},
			{`[1e-400]`, `[0.0]`}, {`[5e-324]`, `[null]`},
			{`[null]`, `[1e-400]`}, {`[1e-400]`, `[]`},
		} {
			rows = append(rows, "("+sqlFixtureString(pair[0])+"::"+string(format)+","+sqlFixtureString(pair[1])+"::"+string(format)+")")
		}
		expected = append(expected, true, true, false, false, true, false)
		fmt.Fprintf(&script, "PREPARE failure_%d AS SELECT %s FROM (VALUES %s) records(\"left\",\"right\"); EXECUTE failure_%d;\n", i, q.Where(), strings.Join(rows, ","), i)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results,want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("Float normalization pair %d: SQL=%s expected=%v", i, result, expected[i])
		}
	}
}
