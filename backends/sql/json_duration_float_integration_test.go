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

func TestPostgreSQLJSONDurationFloatUnionMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.DurationType, schema.FloatType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value hff.Value
		json  string
	}{
		{hff.Duration(time.Nanosecond), `"0.000000001s"`},
		{hff.Duration(0), `"0s"`},
		{hff.Float(1.5), "1.5"},
		{hff.Float(0), "0.0"},
		{hff.Float(math.Copysign(0, -1)), "-0.0"},
		{hff.Null(), "null"},
	}
	sources := []string{
		`value`, `!value`,
		`value:type="duration"`,
		`value:[duration]:nanoseconds=1`,
		`value:[float]>0.5`,
		`value=value`,
		`value:in("1ns":duration,1.5)`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		formatSources := sources
		if format == hffsql.JSON {
			formatSources = append(append([]string(nil), sources...), `value:string="-0.0"`, `value:json="-0.0"`)
		}
		for si, source := range formatSources {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format),
			))
			if err != nil {
				t.Fatal(source, err)
			}
			var parameterTypes, arguments []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				parameterTypes = append(parameterTypes, typ)
				arguments = append(arguments, literal)
			}
			var values []string
			for ri, row := range rows {
				match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": row.value}))
				if err != nil {
					t.Fatal(source, ri, err)
				}
				expected = append(expected, match)
				labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, ri))
				values = append(values, "("+sqlFixtureString(row.json)+"::"+string(format)+")")
			}
			params, args := "", ""
			if len(parameterTypes) > 0 {
				params = "(" + strings.Join(parameterTypes, ",") + ")"
				args = "(" + strings.Join(arguments, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE json_duration_float_%d_%d%s AS SELECT %s FROM (VALUES %s) records(value); EXECUTE json_duration_float_%d_%d%s;\n", fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
		}
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
		if (result == "t") != expected[i] {
			t.Errorf("%s: PostgreSQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}
