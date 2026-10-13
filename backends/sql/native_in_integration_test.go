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

func TestPostgreSQLNativeScalarInMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	sources := []string{
		`age:in(18,21)`,
		`name:in("Alice","Bob",null)`,
		`flag:in(true,false)`,
		`name:in(name!)`,
		`age:in(age,null)`,
		`name:in(null)`,
		`name:in(name,null)`,
		`!name:in("Alice")`,
		`age:in(18,21) name="Alice"`,
		`age:in(18,factor!)`,
		`age:in(18,21):type="null"`,
	}
	ages := []hff.Value{hff.Null(), hff.Int(0), hff.Int(18), hff.Int(21)}
	names := []hff.Value{hff.Null(), hff.String("Alice"), hff.String("Bob")}
	flags := []hff.Value{hff.Null(), hff.Bool(false), hff.Bool(true)}
	var script strings.Builder
	var wants []bool
	var labels []string
	for si, source := range sources {
		program := compile(t, source)
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program,
			hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, lit := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, lit)
		}
		var rows []string
		for _, age := range ages {
			for _, name := range names {
				for _, flag := range flags {
					factor := hff.Null()
					record := hff.Object(map[string]hff.Value{
						"age": age, "name": name, "flag": flag, "factor": factor,
					})
					matched, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(source, err)
					}
					rows = append(rows, "("+strings.Join([]string{
						sqlFixtureValue(t, age, "bigint"),
						sqlFixtureValue(t, name, "text"),
						sqlFixtureValue(t, flag, "boolean"),
						"NULL::double precision",
					}, ",")+")")
					wants = append(wants, matched)
					labels = append(labels, fmt.Sprintf("%s age=%v name=%v flag=%v", source, age, name, flag))
				}
			}
		}
		parameters, arguments := "", ""
		if len(types) != 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script,
			"PREPARE scalar_in_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(age,name,flag,factor); EXECUTE scalar_in_%d%s;\n",
			si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container,
		"psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(wants) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(wants), out)
	}
	for i, result := range results {
		if result == "n" || (result == "true") != wants[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, wants[i])
		}
	}
}

func TestPostgreSQLNativeFloatAndBytesInMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fixtures := []struct {
		typ     hff.Type
		storage string
		values  []hff.Value
		sources []string
	}{
		{schema.FloatType, "double precision",
			[]hff.Value{hff.Null(), hff.Float(0), hff.Float(math.Copysign(0, -1)), hff.Float(1.5), hff.Float(2.5)},
			[]string{`data:in(0.0,1.5)`, `data:in(data!)`, `data:in(1.5,data!)`, `data:in(null)`}},
		{schema.BytesType, "bytea",
			[]hff.Value{hff.Null(), hff.Bytes(nil), hff.Bytes([]byte{0, 1}), hff.Bytes([]byte{255})},
			[]string{`data:in("AAE=":frombase64)`, `data:in(data!)`, `data:in("AAE=":frombase64,data!)`, `data:in(null)`}},
	}
	var script strings.Builder
	var wants []bool
	var labels []string
	for fi, fixture := range fixtures {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(fixture.typ, schema.NullType)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for si, source := range fixture.sources {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))))
			if err != nil {
				t.Fatal(source, err)
			}
			var types, args []string
			for _, arg := range query.Args() {
				typ, lit := sqlFixtureParameter(t, arg)
				types, args = append(types, typ), append(args, lit)
			}
			var rows []string
			for _, value := range fixture.values {
				matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": value}))
				if err != nil {
					t.Fatal(source, err)
				}
				rows = append(rows, "("+sqlFixtureValue(t, value, fixture.storage)+")")
				wants = append(wants, matched)
				labels = append(labels, fmt.Sprintf("%s %s=%v", source, fixture.storage, value))
			}
			parameters, arguments := "", ""
			if len(types) != 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE typed_in_%d_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(data); EXECUTE typed_in_%d_%d%s;\n",
				fi, si, parameters, query.Where(), strings.Join(rows, ","), fi, si, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container,
		"psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(wants) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(wants), out)
	}
	for i, result := range results {
		if result == "n" || (result == "true") != wants[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, wants[i])
		}
	}
}
