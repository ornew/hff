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

func TestPostgreSQLDynamicJSONArithmetic(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("left"), schema.Any("right"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]hff.Value{
		{hff.Int(5), hff.Int(2)}, {hff.Int(-5), hff.Int(2)},
		{hff.Int(9223372036854775807), hff.Int(1)},
		{hff.Int(-9223372036854775808), hff.Int(-1)},
		{hff.Int(9007199254740993), hff.Float(1)},
		{hff.Float(0.1), hff.Float(0.2)},
		{hff.Float(5e-324), hff.Float(2)},
		{hff.Float(-0.0), hff.Int(0)},
		{hff.Int(1), hff.Int(0)}, {hff.Null(), hff.Int(1)},
		{hff.Null(), hff.Null()}, {hff.String("x"), hff.Null()},
		{hff.Bool(false), hff.Int(1)}, {hff.List(), hff.Int(2)},
		{hff.Int(1), hff.Object(nil)},
	}
	sources := []string{}
	for _, op := range []string{"+", "-", "*", "/", "%"} {
		for _, suffix := range []string{`:type="null"`, `:type="number"`, `=2.5`, `=7`, `=-3`, `=1`, `=0`, `=9007199254740992`} {
			sources = append(sources, "(left"+op+"right)"+suffix)
		}
		sources = append(sources, "(left"+op+`right!):type="null"`)
	}
	sources = append(sources, `(-left):type="null"`, `(-left)=-5`, `(-left!):type="null"`)
	var script strings.Builder
	var expected []bool
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range sources {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(err)
			}
			var bindings []hffsql.Binding
			for _, name := range []string{"left", "right"} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
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
			for _, pair := range pairs {
				match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": pair[0], "right": pair[1]}))
				if err != nil {
					t.Fatal(err)
				}
				index := len(expected)
				arguments, parameters := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(literals, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE pair_%d%s AS SELECT %s FROM (VALUES (%s::%s,%s::%s,%s::jsonb,%s::jsonb)) records(\"left\",\"right\",left_meta,right_meta); EXECUTE pair_%d%s;\n", index, parameters, query.Where(), sqlFixtureString(metadataJSONFixture(t, pair[0])), format, sqlFixtureString(metadataJSONFixture(t, pair[1])), format, sqlFixtureString(pairTypeMetadata(t, pair[0])), sqlFixtureString(pairTypeMetadata(t, pair[1])), index, arguments)
				expected = append(expected, match)
				descriptions = append(descriptions, source+" "+string(format)+" "+metadataJSONFixture(t, pair[0])+" / "+metadataJSONFixture(t, pair[1]))
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
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), output)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", descriptions[i], result, expected[i])
		}
	}
}
