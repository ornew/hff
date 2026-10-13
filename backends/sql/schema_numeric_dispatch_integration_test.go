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
)

func TestPostgreSQLSchemaNumericDispatchMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, bindings := numericDispatchEngine(t)
	type record struct{ choice, floating, ceiling, threshold hff.Value }
	records := []record{
		{hff.Null(), hff.Null(), hff.Null(), hff.Null()},
		{hff.String("12"), hff.String("1.5"), hff.Int(10), hff.Float(1.5)},
		{hff.String(""), hff.String(""), hff.Null(), hff.Null()},
		{hff.Int(11), hff.Float(2), hff.Int(10), hff.Float(1.5)},
		{hff.Int(0), hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0)},
		{hff.Int(9007199254740993), hff.Float(9007199254740992), hff.Int(9007199254740993), hff.Float(9007199254740992)},
		{hff.Int(math.MaxInt64), hff.Float(math.MaxFloat64), hff.Int(math.MaxInt64), hff.Float(9223372036854775808)},
		{hff.Int(math.MinInt64), hff.Float(math.SmallestNonzeroFloat64), hff.Int(math.MinInt64), hff.Float(-9223372036854775808)},
	}
	sources := []string{
		`choice>10`, `choice=10`, `choice!=10`, `choice<10`,
		`choice>1.5`, `choice=9007199254740992.0`, `choice>threshold`, `threshold<choice`,
		`choice>ceiling`, `ceiling<choice`,
		`fchoice>1.5`, `fchoice=1.5`, `fchoice!=1.5`, `fchoice<1.5`,
		`fchoice>1`, `fchoice=9007199254740993`, `fchoice<ceiling`, `ceiling>fchoice`,
		`fchoice>threshold`, `threshold<fchoice`,
		`!(choice>10)`, `(choice>10):type="null"`, `(choice>10):exists`,
		`(choice!>10):type="bool"`, `!(choice:[int]>10)`,
		`!(fchoice>1.5)`, `(fchoice>1.5):type="null"`, `(fchoice>1.5):exists`,
		`(fchoice!>1.5):type="bool"`, `!(fchoice:[float]>1.5)`,
		`ceiling>10 choice>10`, `threshold>1.5|choice>10`,
		`!(ceiling>10 fchoice>1.5)`,
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
		for ri, record := range records {
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
				"choice": record.choice, "fchoice": record.floating, "ceiling": record.ceiling, "threshold": record.threshold,
			}))
			if err != nil {
				t.Fatal(source, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			rows = append(rows, "("+sqlFixtureString(metadataJSONFixture(t, record.choice))+"::jsonb,"+
				sqlFixtureString(metadataJSONFixture(t, record.floating))+"::jsonb,"+
				sqlFixtureString(pairTypeMetadata(t, record.floating))+"::jsonb,"+
				sqlFixtureValue(t, record.ceiling, "bigint")+","+sqlFixtureValue(t, record.threshold, "double precision")+")")
		}
		parameters, arguments := "", ""
		if len(types) != 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE numeric_dispatch_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(choice,fchoice,float_meta,ceiling,threshold); EXECUTE numeric_dispatch_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got%d results,want%d:%s", len(results), len(expected), out)
	}
	for i, result := range results {
		if result == "n" || (result == "true") != expected[i] {
			t.Errorf("%s:SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}
