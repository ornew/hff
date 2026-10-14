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

func TestPostgreSQLAverageMatchesLocalForOrdinaryValues(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, tc := range []struct {
		name    string
		element hff.Type
		values  []hff.Value
		sources []string
	}{
		{"integers", schema.IntType,
			[]hff.Value{hff.Null(), hff.List(), hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Int(0), hff.Int(0)), hff.List(hff.Int(math.MaxInt64), hff.Int(math.MaxInt64))},
			[]string{`items:avg=null`, `items:avg=1.5`, `items:avg:json="0.0"`, `items:avg:type="float"`, `items:avg:exists`}},
		{"floats", schema.FloatType,
			[]hff.Value{hff.Null(), hff.List(), hff.List(hff.Float(1), hff.Float(2)), hff.List(hff.Float(0))},
			[]string{`items:avg=null`, `items:avg=1.5`, `items:avg:json="0.0"`, `items:avg:type="float"`}},
		{"mixed", schema.Union(schema.IntType, schema.FloatType, schema.NullType),
			[]hff.Value{hff.Null(), hff.List(), hff.List(hff.Null()), hff.List(hff.Int(1), hff.Float(2.5)), hff.List(hff.Int(1), hff.Null(), hff.Int(3))},
			[]string{`items:avg=null`, `items:avg=1.75`, `items:avg=2.0`, `items:avg(x=>x!)=1.75`}},
		{"any", schema.AnyType,
			[]hff.Value{hff.Null(), hff.List(), hff.List(hff.Int(1), hff.Float(2.5)), hff.List(hff.Int(1), hff.String("invalid"))},
			[]string{`items:avg=null`, `items:avg=1.75`, `items:avg(x=>x)=1.75`}},
		{"projection", schema.Struct(schema.Nullable(schema.Int("price"))),
			[]hff.Value{hff.Null(), hff.List(), hff.List(hff.Object(map[string]hff.Value{"price": hff.Null()})),
				hff.List(hff.Object(map[string]hff.Value{"price": hff.Int(1)}), hff.Object(map[string]hff.Value{"price": hff.Int(2)})),
				hff.List(hff.Object(map[string]hff.Value{"price": hff.Int(1)}), hff.Object(map[string]hff.Value{"price": hff.Null()}), hff.Object(map[string]hff.Value{"price": hff.Int(3)}))},
			[]string{`items:avg(x=>x.price)=1.5`, `items:avg(x=>x.price!)=2.0`, `items:avg(x=>x.price)=null`, `items:avg(x=>x.price!)=null`}},
	} {
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", tc.element)))), engine.WithCapabilities(capabilities.Standard()))
				if err != nil {
					t.Fatal(err)
				}
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				var script strings.Builder
				var expected, labels []string
				for _, source := range tc.sources {
					program, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, program)
					if err != nil {
						t.Fatal(source, err)
					}
					query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
						hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...)))
					if err != nil {
						t.Fatal(source, err)
					}
					if !strings.Contains(query.Where(), "avg(") {
						t.Fatalf("%s did not lower to SQL AVG: %s", source, query.Where())
					}
					var types, literals []string
					for _, arg := range query.Args() {
						typ, literal := sqlFixtureParameter(t, arg)
						types, literals = append(types, typ), append(literals, literal)
					}
					params, args := "", ""
					if len(types) > 0 {
						params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
					}
					for _, value := range tc.values {
						matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": value}))
						if err != nil {
							t.Fatal(source, err)
						}
						want := "f"
						if matched {
							want = "t"
						}
						expected = append(expected, want)
						labels = append(labels, source+" on "+metadataJSONFixture(t, value))
						name := fmt.Sprintf("avg_%d", len(expected))
						fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb)) records(items,metadata); EXECUTE %s%s; DEALLOCATE %s;\n",
							name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, value)), format,
							sqlFixtureString(pairTypeMetadata(t, value)), name, args, name)
					}
				}
				command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				command.Stdin = strings.NewReader(script.String())
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("PostgreSQL average failed: %v\n%s", err, output)
				}
				actual := strings.Fields(string(output))
				if len(actual) != len(expected) {
					t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
				}
				for i, want := range expected {
					if actual[i] != want {
						t.Errorf("%s: PostgreSQL=%s local=%s", labels[i], actual[i], want)
					}
				}
			})
		}
	}
}

func TestPostgreSQLAverageCanAvoidLocalIntermediateOverflow(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:avg:exists`)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		t.Fatal(err)
	}
	value := hff.List(hff.Float(1e308), hff.Float(1e308))
	matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": value}))
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("Local running Float total should overflow")
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Args()) != 0 {
		t.Fatalf("unexpected SQL parameters: %v", query.Args())
	}
	script := "SET extra_float_digits=3; SELECT " + query.Where() + " FROM (VALUES (" + sqlFixtureString(metadataJSONFixture(t, value)) + "::json)) records(items);"
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL average failed: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "t" {
		t.Fatalf("PostgreSQL AVG(numeric) should remain finite; got %q", got)
	}
}
