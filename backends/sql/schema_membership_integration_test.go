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

func TestPostgreSQLSchemaMembershipMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	obj := func(v map[string]hff.Value) hff.Value { return hff.Object(v) }
	number := schema.Union(schema.IntType, schema.FloatType, schema.NullType)
	profiles := []struct {
		name     string
		element  hff.Type
		metadata bool
		pairs    [][2]hff.Value
	}{
		{"overlapping_lists", schema.Union(schema.StringType, schema.NullType, schema.ListOf(schema.NullableType(schema.IntType)), schema.ListOf(schema.NullableType(schema.FloatType))), true, [][2]hff.Value{
			{hff.List(), hff.List()}, {hff.String("x"), hff.String("x")}, {hff.String("1"), hff.List(hff.Int(1))},
			{hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null())},
			{hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Float(2), hff.Float(1))},
			{hff.List(hff.Int(9007199254740993)), hff.List(hff.Float(9007199254740992))},
			{hff.List(hff.Int(math.MaxInt64)), hff.List(hff.Float(9223372036854775808))},
			{hff.List(hff.Float(math.Copysign(0, -1))), hff.List(hff.Int(0))}, {hff.Null(), hff.Null()},
		}},
		{"overlapping_structs", schema.Union(schema.StringType, schema.NullType,
			schema.Struct(schema.Int("i"), schema.Optional(schema.Nullable(schema.Float("f")))), schema.Struct(schema.Int("i"), schema.Optional(schema.String("label")))), false, [][2]hff.Value{
			{obj(map[string]hff.Value{"i": hff.Int(1)}), obj(map[string]hff.Value{"i": hff.Int(1)})},
			{obj(map[string]hff.Value{"i": hff.Int(1)}), obj(map[string]hff.Value{"i": hff.Int(1), "f": hff.Null()})},
			{obj(map[string]hff.Value{"i": hff.Int(1), "f": hff.Float(1e20)}), obj(map[string]hff.Value{"f": hff.Float(1e20), "i": hff.Int(1)})},
			{obj(map[string]hff.Value{"i": hff.Int(1), "f": hff.Float(1)}), obj(map[string]hff.Value{"i": hff.Int(1), "label": hff.String("1")})},
			{hff.Null(), obj(map[string]hff.Value{"i": hff.Int(1)})},
		}},
		{"map_and_struct", schema.Union(schema.StringType, schema.NullType, schema.MapOf(number), schema.Struct(schema.Float("fixed"))), true, [][2]hff.Value{
			{obj(nil), obj(nil)}, {obj(nil), obj(map[string]hff.Value{"a": hff.Null()})},
			{obj(map[string]hff.Value{"": hff.Int(1)}), obj(map[string]hff.Value{"": hff.Float(1)})},
			{obj(map[string]hff.Value{"fixed": hff.Float(1), "tail": hff.Int(2)}), obj(map[string]hff.Value{"tail": hff.Float(2), "fixed": hff.Int(1)})},
			{obj(map[string]hff.Value{"a/b~\n": hff.Int(9007199254740993)}), obj(map[string]hff.Value{"a/b~\n": hff.Float(9007199254740992)})},
		}},
		{"nested_any", schema.Union(schema.StringType, schema.NullType, schema.Struct(schema.Int("id"), schema.Any("payload"))), true, [][2]hff.Value{
			{obj(map[string]hff.Value{"id": hff.Int(1), "payload": hff.List(hff.Int(1), hff.Null())}), obj(map[string]hff.Value{"id": hff.Int(1), "payload": hff.List(hff.Float(1), hff.Null())})},
			{obj(map[string]hff.Value{"id": hff.Int(1), "payload": hff.List(hff.Int(9007199254740993))}), obj(map[string]hff.Value{"id": hff.Int(1), "payload": hff.List(hff.Float(9007199254740992))})},
			{obj(map[string]hff.Value{"id": hff.Int(1), "payload": obj(nil)}), obj(map[string]hff.Value{"id": hff.Int(1), "payload": obj(map[string]hff.Value{"x": hff.Null()})})},
		}},
		{"nested_lists_and_never", schema.Union(schema.StringType, schema.NullType, schema.ListOf(schema.ListOf(number)), schema.ListOf(schema.NeverType), schema.Struct()), true, [][2]hff.Value{
			{hff.List(), hff.List()}, {hff.List(), obj(nil)}, {obj(nil), obj(nil)},
			{hff.List(hff.List(hff.Int(1), hff.Null())), hff.List(hff.List(hff.Float(1), hff.Null()))},
			{hff.List(hff.List(hff.Int(1)), hff.List(hff.Int(2))), hff.List(hff.List(hff.Int(2)), hff.List(hff.Int(1)))},
		}},
	}
	sources := []string{
		`value:in(needle)`, `!value:in(needle)`, `(value:in(needle)):type="null"`,
		`value!:in(needle)`, `!(value!:in(needle))`, `items!:contains(needle!)`,
		`items:[list[any]]:contains(needle)`, `items:any(x=>x!:in(needle))`,
		`items:contains(needle)`, `!items:contains(needle)`, `items:contains(needle,null)`, `items:contains(null,guard!.value)`,
		`items:unique`, `!items:unique`, `items:unique:type="null"`, `items:in(other)`,
		`value:in(needle,guard!.value)`, `items:any(value=>value:in(needle))`,
		`groups:any(xs=>xs:contains(needle))`, `items:map(x=>x):contains(needle)`, `items:map(x=>x):unique`,
		`groups:any(items=>items:contains(needle) groups:any(items=>items:unique))`,
		`items:foldl(needle,(acc,x)=>x):in(needle)`, `items:foldr(needle,(x,acc)=>x):in(needle)`,
	}
	fields := []string{"value", "needle", "items", "other", "groups", "guard"}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, profile := range profiles {
		e := schemaMembershipEngine(t, profile.element)
		var records []hff.Value
		for pi, pair := range profile.pairs {
			for variant := 0; variant < 3; variant++ {
				items, other := hff.List(pair[0], pair[1]), hff.List(pair[1], pair[0])
				guard := hff.Null()
				if variant == 1 {
					items, other = hff.List(pair[0], pair[0]), hff.List(pair[0], pair[0])
					guard = obj(map[string]hff.Value{"value": pair[1]})
				} else if variant == 2 {
					items, other = hff.List(), hff.List()
					if pi%2 == 0 {
						items = hff.Null()
					}
				}
				records = append(records, obj(map[string]hff.Value{"value": pair[0], "needle": pair[1], "items": items, "other": other, "groups": hff.List(hff.List(pair[0]), hff.List(pair[1]), hff.List()), "guard": guard}))
			}
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var rows []string
			var columns []string
			columns = append(columns, fields...)
			for _, field := range fields {
				columns = append(columns, field+"_meta")
			}
			for ri, record := range records {
				values, _ := record.AsObject()
				var row []string
				for _, field := range fields {
					code := sqlFixtureString(metadataJSONFixture(t, values[field]))
					if values[field].Kind() == hff.NullKind && ri%2 == 0 {
						code = "NULL"
					}
					row = append(row, code+"::"+string(format))
				}
				for _, field := range fields {
					row = append(row, sqlFixtureString(pairTypeMetadata(t, values[field]))+"::jsonb")
				}
				rows = append(rows, "("+strings.Join(row, ",")+")")
			}
			for _, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(profile.name, source, err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q := schemaMembershipQuery(t, e, source, format, format == hffsql.JSONB && profile.metadata)
				var types, args []string
				for _, value := range q.Args() {
					typ, literal := sqlFixtureParameter(t, value)
					types, args = append(types, typ), append(args, literal)
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
				}
				si := len(expected)
				fmt.Fprintf(&script, "PREPARE membership_%d%s AS SELECT %s FROM (VALUES %s) records(%s); EXECUTE membership_%d%s;\n", si, parameters, q.Where(), strings.Join(rows, ","), strings.Join(columns, ","), si, arguments)
				for ri, record := range records {
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(profile.name, source, ri, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s/%s %s record%d", profile.name, format, source, ri))
				}
			}
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d schema membership comparisons", len(expected))
}

// A rejected PostgreSQL Float decode is nested null after the existing dynamic
// normalization path. Preserve that state when consuming the raw container.
func TestPostgreSQLSchemaMembershipFloatFailureIsNestedNull(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e := schemaMembershipEngine(t, schema.Union(schema.StringType, schema.ListOf(schema.NullableType(schema.IntType)), schema.ListOf(schema.NullableType(schema.FloatType))))
	var script strings.Builder
	var labels []string
	for _, token := range []string{"1e400", "1e-400", "-1e-400"} {
		for _, source := range []string{`value:in(needle)`, `items:contains(needle)`, `!items:unique`, `items:in(other)`} {
			q := schemaMembershipQuery(t, e, source, hffsql.JSON, false)
			if len(q.Args()) != 0 {
				t.Fatal(source, q.Args())
			}
			fmt.Fprintf(&script, "SELECT %s FROM (VALUES ('[%s]'::json,'[null]'::json,'[[%s],[null]]'::json,'[[null],[null]]'::json)) records(value,needle,items,other);\n", q.Where(), token, token)
			labels = append(labels, source+" "+token)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(labels) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(labels), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s lost nested null state: %s", labels[i], result)
		}
	}
}

func TestPostgreSQLSchemaMembershipKeepsJSONBNonnumericBypass(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	left := schema.Union(schema.StringType, schema.ListOf(schema.FloatType), schema.ListOf(schema.NullType))
	right := schema.Union(schema.StringType, schema.ListOf(schema.NullType))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", left), schema.Field("needle", right), schema.List("items", left), schema.List("other", right), schema.List("groups", schema.ListOf(left)), schema.Nullable(schema.Field("guard", schema.Struct(schema.Field("value", left)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []string
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, token := range []string{"1e-400", "-1e-400", "1e400"} {
			for _, source := range []string{`value:in(needle)`, `needle:in(value)`, `items:contains(needle)`, `items:in(other)`} {
				q := schemaMembershipQuery(t, e, source, format, false)
				if len(q.Args()) != 0 {
					t.Fatal(source, q.Args())
				}
				fmt.Fprintf(&script, "SELECT %s FROM (VALUES ('[%s]'::%s,'[null]'::%s,'[[%s]]'::%s,'[[null]]'::%s)) records(value,needle,items,other);\n", q.Where(), token, format, format, token, format, format)
				want := "t"
				if format == hffsql.JSONB {
					want = "f"
				}
				expected = append(expected, want)
				labels = append(labels, string(format)+" "+source+" "+token)
			}
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got%d results, want%d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if result != expected[i] {
			t.Errorf("%s SQL=%s want=%s", labels[i], result, expected[i])
		}
	}
}
