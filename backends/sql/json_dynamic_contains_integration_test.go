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

func TestPostgreSQLDynamicContains(t *testing.T) {
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
		{hff.String("abc"), hff.String("b")}, {hff.String("abc"), hff.String("z")},
		{hff.String("abc"), hff.Null()}, {hff.String("abc"), hff.Int(1)},
		{hff.Null(), hff.String("a")}, {hff.Int(1), hff.String("a")},
		{hff.List(hff.Int(1), hff.Float(2), hff.Null(), hff.String("abc")), hff.Int(2)},
		{hff.List(hff.Int(1), hff.Float(2), hff.Null(), hff.String("abc")), hff.Null()},
		{hff.List(hff.Int(1), hff.Float(2), hff.Null(), hff.String("abc")), hff.String("abc")},
		{hff.List(hff.Int(9007199254740993)), hff.Float(9007199254740992)},
		{hff.List(hff.Int(1), hff.Float(1)), hff.Int(1)},
		{hff.List(), hff.Null()},
		{hff.List(hff.Object(map[string]hff.Value{"x": hff.Int(1), "y": hff.Null()})), hff.Object(map[string]hff.Value{"y": hff.Null(), "x": hff.Float(1)})},
		{hff.List(hff.List(hff.Int(1), hff.Null())), hff.List(hff.Float(1), hff.Null())},
	}
	sources := []string{`data:contains(needle)`, `data:contains(needle,null)`, `data:contains(needle):type="null"`, `data:contains(null,guard!.name):type="null"`, `data:contains("z",guard!.name):type="null"`, `data:contains("z",null):type="null"`, `data:[list[any]]:contains(needle)`, `data:[string]:contains(needle)`, `needle:in(data)`, `data:[list[any]]:unique`, `data:contains(needle,guard!.name):exists`, `data:contains(1,guard!.name):type="null"`}
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
				fmt.Fprintf(&script, "PREPARE contains_%d%s AS SELECT %s FROM (VALUES (%s::%s,%s::%s,'null'::%s,%s::jsonb,%s::jsonb)) records(data,needle,guard,data_meta,needle_meta); EXECUTE contains_%d%s;\n", index, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, pair[0])), format, sqlFixtureString(metadataJSONFixture(t, pair[1])), format, format, sqlFixtureString(pairTypeMetadata(t, pair[0])), sqlFixtureString(pairTypeMetadata(t, pair[1])), index, args)
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

func TestPostgreSQLContainsWithoutNumericMetadata(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx := context.Background()
	stringList := schema.Union(schema.StringType, schema.ListOf(schema.StringType))
	objects := schema.Union(schema.Struct(schema.Int("a"), schema.Float("b")), schema.Struct(schema.Int("a"), schema.Float("b"), schema.Bool("c")))
	for _, tc := range []struct {
		source       string
		typ          hff.Type
		data, needle hff.Value
		raw          string
	}{
		{`data:contains("foo")`, schema.AnyType, hff.List(hff.Float(1e20), hff.String("foo")), hff.Null(), ""},
		{`data:contains([])`, schema.AnyType, hff.List(hff.List(hff.Float(1e20))), hff.Null(), ""},
		{`data:contains({"x":null})`, schema.AnyType, hff.List(hff.Object(map[string]hff.Value{"x": hff.Float(1e20)})), hff.Null(), ""},
		{`data:contains(null)`, schema.AnyType, hff.List(hff.Float(1e20)), hff.Null(), ""},
		{`data:contains(needle)`, stringList, hff.List(hff.String("foo")), hff.String("foo"), ""},
		{`data:contains(needle)`, stringList, hff.List(hff.String("foo")), hff.Float(1e20), ""},
		{`data:contains(needle)`, stringList, hff.String("foobar"), hff.String("foo"), ""},
		{`data:contains(needle):type="null"`, stringList, hff.String("foobar"), hff.Float(1e20), ""},
		{`data:contains(1):type="null"`, schema.Union(schema.ListOf(schema.IntType), schema.IntType, schema.FloatType), hff.Float(1e20), hff.Null(), ""},
		{`data:contains({"a":1,"b":9223372036854775808.0})`, schema.ListOf(objects), hff.List(hff.Object(map[string]hff.Value{"a": hff.Int(1), "b": hff.Float(9223372036854775808)})), hff.Null(), `[{"a":1,"b":9223372036854775808}]`},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ), schema.Any("needle"))), engine.WithCapabilities(capabilities.Standard()))
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

func TestPostgreSQLDynamicLabelRecipe(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx := context.Background()
	labelType := schema.Union(schema.StringType, schema.ListOf(schema.StringType))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", labelType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `labels.team:contains("platform") labels.environment="production"`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("services", "labels"), hffsql.JSONB)))
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		t.Fatal(err)
	}
	var rows, expected []string
	for i, pair := range [][2]hff.Value{
		{hff.String("platform"), hff.String("production")},
		{hff.List(hff.String("platform"), hff.String("on-call")), hff.String("production")},
		{hff.String("internal"), hff.String("production")},
		{hff.List(hff.String("platform")), hff.String("staging")},
	} {
		labels := hff.Object(map[string]hff.Value{"team": pair[0], "environment": pair[1]})
		match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"labels": labels}))
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprint(i + 1)
		if match {
			expected = append(expected, id)
		}
		rows = append(rows, "("+id+","+sqlFixtureString(metadataJSONFixture(t, labels))+"::jsonb)")
	}
	if strings.Join(expected, ",") != "1,2" {
		t.Fatalf("local recipeIDs=%v", expected)
	}
	var types, literals []string
	for _, arg := range query.Args() {
		typ, literal := sqlFixtureParameter(t, arg)
		types = append(types, typ)
		literals = append(literals, literal)
	}
	script := fmt.Sprintf("PREPARE labels(%s) AS SELECT services.id FROM (VALUES %s) services(id,labels) WHERE %s ORDER BY services.id; EXECUTE labels(%s);", strings.Join(types, ","), strings.Join(rows, ","), query.Where(), strings.Join(literals, ","))
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil || strings.Join(strings.Fields(string(output)), ",") != strings.Join(expected, ",") {
		t.Fatalf("recipeIDs want%v, got%s (%v)", expected, output, err)
	}
}
