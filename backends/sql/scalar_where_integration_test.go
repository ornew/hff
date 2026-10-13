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

func TestPostgreSQLDirectScalarWhereMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.Nullable(schema.Int("age")),
			schema.Nullable(schema.String("name")),
			schema.Nullable(schema.Bool("active")),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
		hffsql.Bind(hff.Path{"active"}, hffsql.Column("records", "active")),
	}
	ages := []hff.Value{hff.Null(), hff.Int(0), hff.Int(18), hff.Int(19)}
	names := []hff.Value{hff.Null(), hff.String("Alice"), hff.String("Bob"), hff.String("é")}
	actives := []hff.Value{hff.Null(), hff.Bool(false), hff.Bool(true)}
	sources := []string{
		`age>18 name="Alice"`,
		`age>18|name="Alice"`,
		`(age>18 name="Alice")|active=true`,
		`active!=false name<"é"`,
		`age=null`,
		`age!=null`,
		`active`,
		`!active`,
		`age`,
		`!age`,
		`name`,
		`!name`,
		`age|name`,
		`!age>18`,
		`!(age>18|name="Alice")`,
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
		for _, age := range ages {
			for _, name := range names {
				for _, active := range actives {
					record := hff.Object(map[string]hff.Value{"age": age, "name": name, "active": active})
					want, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(source, err)
					}
					rows = append(rows, "("+strings.Join([]string{
						sqlFixtureValue(t, age, "bigint"),
						sqlFixtureValue(t, name, "text"),
						sqlFixtureValue(t, active, "boolean"),
					}, ",")+")")
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s age=%v name=%v active=%v", source, age, name, active))
				}
			}
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE scalar_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(age,name,active); EXECUTE scalar_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
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
