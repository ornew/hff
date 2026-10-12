//go:build integration

package sql_test

import (
	"context"
	"fmt"
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

func TestPostgreSQLDynamicShape(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"), schema.Any("needle"), schema.Nullable(schema.Field("guard", schema.Struct(schema.String("name")))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	cases := [][2]hff.Value{
		{hff.Object(map[string]hff.Value{"a": hff.Null(), "b": hff.Int(1)}), hff.String("a")},
		{hff.Object(map[string]hff.Value{"a": hff.Null(), "b": hff.Float(1)}), hff.String("missing")},
		{hff.Object(map[string]hff.Value{"é": hff.Int(1), "z": hff.Int(2)}), hff.Int(0)},
		{hff.Object(map[string]hff.Value{}), hff.String("a")},
		{hff.List(hff.Int(1), hff.Null()), hff.Int(0)},
		{hff.List(hff.Int(1), hff.Null()), hff.Int(-1)},
		{hff.List(hff.Int(1), hff.Null()), hff.Int(-2)},
		{hff.List(hff.Int(1), hff.Null()), hff.Int(-3)},
		{hff.List(hff.Int(1), hff.Null()), hff.Float(1)},
		{hff.List(hff.Int(1), hff.Null()), hff.Float(0.5)},
		{hff.List(hff.Int(1)), hff.Int(9223372036854775807)},
		{hff.List(hff.Int(1)), hff.Float(1e20)},
		{hff.List(hff.Int(1)), hff.Float(9223372036854775808)},
		{hff.List(hff.Int(1)), hff.Float(-9223372036854775808)},
		{hff.List(hff.Int(1)), hff.Int(-9223372036854775808)},
		{hff.List(), hff.Int(0)},
		{hff.List(hff.Int(1)), hff.String("0")},
		{hff.String("abc"), hff.String("a")},
		{hff.Int(1), hff.Null()},
		{hff.Null(), hff.Int(0)},
		{hff.List(hff.Int(1)), hff.Null()},
	}
	sources := []string{`data:size=2`, `data:size:type="null"`, `data:has(needle)`, `data:has(needle):type="null"`, `data:keys=["a","b"]`, `data:keys=["z","é"]`, `data:keys:type="null"`, `data:has(guard!.name):type="null"`, `data:[list[any]]:has(needle)`, `data:[object]:has(needle)`, `data:keys:contains("a")`}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range sources {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(err)
			}
			var bindings []hffsql.Binding
			for _, name := range []string{"data", "needle", "guard"} {
				var options []hffsql.JSONBindingOption

				if format == hffsql.JSONB && name == "needle" {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "needle_meta")))
				}
				bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if err != nil {
				t.Fatal(source, err)
			}
			var types, literals []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				literals = append(literals, literal)
			}
			params, args := "", ""
			if len(types) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				args = "(" + strings.Join(literals, ",") + ")"
			}
			for _, pair := range cases {
				match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": pair[0], "needle": pair[1], "guard": hff.Null()}))
				if err != nil {
					t.Fatal(source, err)
				}
				index := len(expected)
				fmt.Fprintf(&script, "PREPARE shape_%d%s AS SELECT %s FROM (VALUES (%s::%s,%s::%s,'null'::%s,%s::jsonb,%s::jsonb)) records(data,needle,guard,data_meta,needle_meta); EXECUTE shape_%d%s;\n", index, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, pair[0])), format, sqlFixtureString(metadataJSONFixture(t, pair[1])), format, format, sqlFixtureString(pairTypeMetadata(t, pair[0])), sqlFixtureString(pairTypeMetadata(t, pair[1])), index, args)
				expected = append(expected, match)
				labels = append(labels, source+" on "+string(format)+" "+metadataJSONFixture(t, pair[0])+" / "+metadataJSONFixture(t, pair[1]))
			}
		}
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(expected) {
		t.Fatalf("got%d results, want%d: %s", len(results), len(expected), output)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}

func TestPostgreSQLTypedHasFloatBounds(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		source       string
		typ          hff.Type
		data, needle hff.Value
		raw          string
	}{
		{`data:has(needle):type="null"`, schema.ListOf(schema.IntType), hff.List(hff.Int(1)), hff.Float(1e20), ""},
		{`data:has(needle):type="null"`, schema.ListOf(schema.IntType), hff.List(hff.Int(1)), hff.Float(9223372036854775808), ""},
		{`data:has(needle):type="null"`, schema.ListOf(schema.IntType), hff.List(hff.Int(1)), hff.Float(-9223372036854775808), ""},
		{`data:has(needle)`, schema.ListOf(schema.IntType), hff.List(hff.Int(1)), hff.Float(0), ""},
	} {
		needleType := schema.FloatType
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ), schema.Field("needle", needleType))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(err)
		}
		want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": tc.data, "needle": tc.needle}))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format), hffsql.BindJSON(hff.Path{"needle"}, hffsql.Column("records", "needle"), format)))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			var types, literals []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				literals = append(literals, literal)
			}
			params, args := "", ""
			if len(types) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				args = "(" + strings.Join(literals, ",") + ")"
			}
			raw := tc.raw
			if raw == "" {
				raw = metadataJSONFixture(t, tc.data)
			}
			script := fmt.Sprintf("PREPARE no_metadata%s AS SELECT %s FROM (VALUES (%s::%s,%s::%s)) records(data,needle); EXECUTE no_metadata%s;", params, query.Where(), sqlFixtureString(raw), format, sqlFixtureString(metadataJSONFixture(t, tc.needle)), format, args)
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script)
			output, err := command.CombinedOutput()
			if err != nil || (strings.TrimSpace(string(output)) == "t") != want {
				t.Fatalf("%s %s: local%v SQL%s (%v)", tc.source, format, want, output, err)
			}
		}
	}
}
