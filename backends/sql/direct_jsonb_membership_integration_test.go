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

func TestPostgreSQLDirectJSONBStringMembershipMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.List("tags", schema.StringType)),
		schema.Nullable(schema.String("name")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
	}
	lists := []hff.Value{
		hff.Null(), hff.List(), hff.List(hff.String("auth")),
		hff.List(hff.String("auth"), hff.String("dev"), hff.String("auth")),
		hff.List(hff.String("")), hff.List(hff.String("é"), hff.String("e\u0301")),
		hff.List(hff.String("%"), hff.String(`a"b`), hff.String(`\`)),
	}
	sources := []string{
		`tags:contains("auth")`, `tags:contains("auth","dev")`,
		`tags:contains("auth","auth")`, `tags:contains("")`,
		`tags:contains("é")`, `tags:contains("e\u0301")`,
		`tags:contains("%")`, `tags:contains("a\"b")`, `tags:contains("\\")`,
		`!tags:contains("auth")`,
		`tags:contains("auth") name="Alice"`,
		`tags:contains("auth")|name="Alice"`,
		`!(tags:contains("auth")|name="Alice")`,
		`tags:contains(null)`, `tags:contains("auth",null)`,
		`tags:contains(name)`, `tags!:contains("auth")`,
		`!tags!:contains("auth")`, `tags:contains("auth"):type="null"`,
	}
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
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, literal)
		}
		var rows []string
		for _, tags := range lists {
			storage := []string{sqlFixtureString(metadataJSONFixture(t, tags)) + "::jsonb"}
			if tags.Kind() == hff.NullKind {
				storage = append(storage, "NULL::jsonb")
			}
			for _, physical := range storage {
				for _, name := range []hff.Value{hff.Null(), hff.String("Alice"), hff.String("auth")} {
					want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"tags": tags, "name": name}))
					if err != nil {
						t.Fatal(source, err)
					}
					rows = append(rows, "("+physical+","+sqlFixtureValue(t, name, "text")+")")
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s tags=%s name=%v", source, physical, name))
				}
			}
		}
		parameters, arguments := "", ""
		if len(types) != 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE jsonb_membership_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(tags,name); EXECUTE jsonb_membership_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
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
		if result == "n" || (result == "true") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}
