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

func TestPostgreSQLDirectJSONBPredicatesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Map("payload", schema.NullableType(schema.StringType))),
		schema.Nullable(schema.List("tags", schema.StringType)),
		schema.Nullable(schema.Map("flags", schema.NullableType(schema.BoolType))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "payload"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"flags"}, hffsql.Column("records", "flags"), hffsql.JSONB),
	}
	fixtures := []struct {
		payload, tags, flags string
		record               hff.Value
	}{
		{"NULL::jsonb", "NULL::jsonb", "NULL::jsonb", hff.Object(map[string]hff.Value{"payload": hff.Null(), "tags": hff.Null(), "flags": hff.Null()})},
		{`'null'::jsonb`, `'null'::jsonb`, `'null'::jsonb`, hff.Object(map[string]hff.Value{"payload": hff.Null(), "tags": hff.Null(), "flags": hff.Null()})},
		{`'{}'::jsonb`, `'[]'::jsonb`, `'{}'::jsonb`, hff.Object(map[string]hff.Value{"payload": hff.Object(nil), "tags": hff.List(), "flags": hff.Object(nil)})},
		{`'{"team":null}'::jsonb`, `'["auth"]'::jsonb`, `'{"state":null}'::jsonb`, hff.Object(map[string]hff.Value{"payload": hff.Object(map[string]hff.Value{"team": hff.Null()}), "tags": hff.List(hff.String("auth")), "flags": hff.Object(map[string]hff.Value{"state": hff.Null()})})},
		{`'{"team":"auth"}'::jsonb`, `'["auth","auth"]'::jsonb`, `'{"state":false}'::jsonb`, hff.Object(map[string]hff.Value{"payload": hff.Object(map[string]hff.Value{"team": hff.String("auth")}), "tags": hff.List(hff.String("auth"), hff.String("auth")), "flags": hff.Object(map[string]hff.Value{"state": hff.Bool(false)})})},
		{`'{"team":"dev"}'::jsonb`, `'["auth","dev"]'::jsonb`, `'{"state":true}'::jsonb`, hff.Object(map[string]hff.Value{"payload": hff.Object(map[string]hff.Value{"team": hff.String("dev")}), "tags": hff.List(hff.String("auth"), hff.String("dev")), "flags": hff.Object(map[string]hff.Value{"state": hff.Bool(true)})})},
		{`'{"a/b":"auth","東京":"auth"}'::jsonb`, `'[""]'::jsonb`, `'{"state":false}'::jsonb`, hff.Object(map[string]hff.Value{"payload": hff.Object(map[string]hff.Value{"a/b": hff.String("auth"), "東京": hff.String("auth")}), "tags": hff.List(hff.String("")), "flags": hff.Object(map[string]hff.Value{"state": hff.Bool(false)})})},
	}
	sources := []string{
		`payload.team="auth"`, `!payload.team="auth"`, `payload.team!="auth"`,
		"payload.`a/b`=\"auth\"", "payload.`東京`=\"auth\"", `flags.state=false`, `!flags.state=false`,
		`tags:all(tag=>tag="auth")`, `!tags:all(tag=>tag="auth")`,
		`tags:all(tag=>"auth"=tag)`, `payload.team="auth" tags:all(tag=>tag="auth")`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for index, source := range sources {
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
		for i, fixture := range fixtures {
			want, err := filter.Match(ctx, fixture.record)
			if err != nil {
				t.Fatal(source, i, err)
			}
			rows = append(rows, "("+fixture.payload+","+fixture.tags+","+fixture.flags+")")
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s fixture %d", source, i))
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE direct_jsonb_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(payload,tags,flags); EXECUTE direct_jsonb_%d%s;\n",
			index, parameters, query.Where(), strings.Join(rows, ","), index, arguments)
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
