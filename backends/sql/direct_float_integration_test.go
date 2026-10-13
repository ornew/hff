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

func TestPostgreSQLDirectFloatPredicatesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Float("factor")), schema.Nullable(schema.Int("age")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.Bind(hff.Path{"factor"}, hffsql.Column("records", "factor")),
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
	}
	factors := []hff.Value{hff.Null()}
	for _, value := range []float64{
		0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
		math.MaxFloat64, -math.MaxFloat64, 0.5, -0.5, 1.5,
		math.Nextafter(1.5, 0), math.Nextafter(1.5, math.Inf(1)),
		9007199254740992, 9007199254740994, 9223372036854775808, -9223372036854775808,
	} {
		factors = append(factors, hff.Float(value))
	}
	ages := []hff.Value{hff.Null(), hff.Int(0), hff.Int(19), hff.Int(9007199254740993), hff.Int(math.MaxInt64), hff.Int(math.MinInt64)}
	sources := []string{
		`factor=1.5`, `factor!=1.5`, `factor<1.5`, `factor<=1.5`, `factor>1.5`, `factor>=1.5`,
		`factor=-0.0`, `factor!=0.0`, `factor<5e-324`, `factor>-5e-324`,
		`factor=1.7976931348623157e308`, `factor>=-1.7976931348623157e308`,
		`factor`, `!factor`, `factor=null`, `factor!=null`, `!factor<1.5`,
		`age>18 factor<1.5`, `age>18|factor<1.5`, `!(age>18|factor<1.5)`,
		`factor:in(0.0,-0.0,1.5,null)`, `!factor:in(0.0,null)`,
		`factor=9007199254740993`, `factor>9223372036854775807`, `factor=-9223372036854775808`,
		`factor=age`, `factor:in(9007199254740993)`, `factor!>1.5`, `!factor!>1.5`,
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
		for _, factor := range factors {
			for _, age := range ages {
				want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"factor": factor, "age": age}))
				if err != nil {
					t.Fatal(source, err)
				}
				rows = append(rows, "("+sqlFixtureValue(t, factor, "double precision")+","+sqlFixtureValue(t, age, "bigint")+")")
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s factor=%v age=%v", source, factor, age))
			}
		}
		parameters, arguments := "", ""
		if len(types) != 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE float_predicate_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(factor,age); EXECUTE float_predicate_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
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
