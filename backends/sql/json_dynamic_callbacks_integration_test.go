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

func TestPostgreSQLDynamicCallbacks(t *testing.T) {
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
		{hff.List(hff.Int(1), hff.Float(2)), hff.Int(1)},
		{hff.List(hff.Int(0), hff.Null()), hff.Int(0)},
		{hff.List(hff.Float(1), hff.Int(1)), hff.Int(1)},
		{hff.List(), hff.String("a")},
		{hff.List(hff.List(hff.Int(1)), hff.List(hff.Float(1))), hff.Int(1)},
		{hff.List(hff.List(hff.Null()), hff.List()), hff.Null()},
		{hff.List(hff.List(hff.Float(1e20)), hff.List(hff.Int(9007199254740993))), hff.Int(9007199254740993)},
		{hff.List(hff.Object(map[string]hff.Value{"name": hff.String("a")}), hff.Null()), hff.String("a")},
		{hff.List(hff.String("a"), hff.String("b")), hff.String("a")},
		{hff.List(hff.Bool(false), hff.Bool(true)), hff.Bool(true)},
		{hff.Null(), hff.Null()},
		{hff.String("a"), hff.String("a")},
		{hff.Object(map[string]hff.Value{"name": hff.String("a")}), hff.String("a")},
	}
	sources := []string{
		`data:any(x=>x)`, `data:all(x=>x)`,
		`data:any(x=>x:[float]:exists)`,
		`data:map(x=>x):exists`, `data:map(x=>x)[0]:[float]:exists`,
		`data:map(x=>x):concat([]):size=2`,
		`data:flatmap(x=>x):exists`, `data:flatmap(x=>x):type="null"`,
		`data:flatmap(x=>x)[0]:[float]:exists`,
		`data:map(x=>x!.name):type="null"`,
		`data:any(x=>x!.name="a")`,
		`data:any(x=>x:any(y=>y=needle))`,
		`data:map(x=>null)=[null,null]`,
		`data:map(x=>guard!.name):type="null"`,
		`data:any(x=>guard!.name):type="null"`,
		`data:flatmap(x=>[x,x]):size=4`,
	}
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
				if format == hffsql.JSONB && name != "guard" {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
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
				fmt.Fprintf(&script, "PREPARE callback_%d%s AS SELECT %s FROM (VALUES (%s::%s,%s::%s,'null'::%s,%s::jsonb,%s::jsonb)) records(data,needle,guard,data_meta,needle_meta); EXECUTE callback_%d%s;\n", index, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, pair[0])), format, sqlFixtureString(metadataJSONFixture(t, pair[1])), format, format, sqlFixtureString(pairTypeMetadata(t, pair[0])), sqlFixtureString(pairTypeMetadata(t, pair[1])), index, args)
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

func TestPostgreSQLCallbacksWithoutMetadata(t *testing.T) {
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
		{`data:any(x=>x:exists)`, schema.AnyType, hff.List(hff.Float(1), hff.Int(2)), hff.String("unused"), `[1,2]`},
		{`data:all(x=>x:exists)`, schema.AnyType, hff.List(hff.Null(), hff.Int(2)), hff.String("unused"), `[null,2]`},
		{`data:map(x=>"fixed")=["fixed","fixed"]`, schema.AnyType, hff.List(hff.Float(1), hff.Int(2)), hff.String("unused"), `[1,2]`},
		{`data:flatmap(x=>[1])=[1,1]`, schema.AnyType, hff.List(hff.Float(1), hff.Int(2)), hff.String("unused"), `[1,2]`},
		{`data:map(x=>"fixed"):type="null"`, schema.AnyType, hff.Int(1), hff.String("unused"), `1`},
		{`data:flatmap(x=>x):type="null"`, schema.ListOf(schema.Union(schema.ListOf(schema.IntType), schema.IntType, schema.FloatType)), hff.List(hff.Float(1e20)), hff.String("unused"), `[100000000000000000000]`},
		{`data:flatmap(x=>x)=[1,2]`, schema.ListOf(schema.Union(schema.ListOf(schema.IntType), schema.IntType, schema.FloatType)), hff.List(hff.List(hff.Int(1)), hff.List(hff.Int(2))), hff.String("unused"), `[[1],[2]]`},
		{`data:map(x=>x)=["a","b"]`, schema.Union(schema.ListOf(schema.StringType), schema.ListOf(schema.BoolType)), hff.List(hff.String("a"), hff.String("b")), hff.String("unused"), `["a","b"]`},
	} {
		needleType := schema.StringType
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
