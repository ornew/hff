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

func TestPostgreSQLJSONTimestampDisjointUnionsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	timestamp := hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC))
	const timestampJSON = `"2026-01-01T00:00:00.000000123Z"`
	for _, tc := range []struct {
		name    string
		member  hff.Type
		literal string
		values  []hff.Value
		json    []string
	}{
		{"bool", schema.BoolType, "true", []hff.Value{timestamp, hff.Bool(true), hff.Bool(false), hff.Null()}, []string{timestampJSON, "true", "false", "null"}},
		{"float", schema.FloatType, "1.5", []hff.Value{timestamp, hff.Float(1.5), hff.Float(math.Copysign(0, -1)), hff.Null()}, []string{timestampJSON, "1.5", "-0.0", "null"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			union := schema.Union(schema.TimestampType, tc.member, schema.NullType)
			e, err := engine.New(engine.WithSchema(schema.Struct(
				schema.Field("value", union),
				schema.List("items", union),
			)), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			sources := []string{
				`value`, `!value`,
				`value:type="timestamp"`,
				`value:[timestamp]:year=2026`,
				`value=value`,
				`value=` + tc.literal,
				`value:in("2026-01-01T00:00:00.000000123Z":timestamp,` + tc.literal + `)`,
				`items:contains(` + tc.literal + `)`,
				`items:unique`,
				`items:map(x=>x:type):contains("timestamp")`,
			}
			if tc.name == "float" {
				sources = append(sources, `value:type="number"`, `value:[float]:exists`)
			} else {
				sources = append(sources, `value:type="bool"`, `value:[bool]:exists`)
			}
			var script strings.Builder
			var expected []bool
			var labels []string
			for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				formatSources := sources
				if format == hffsql.JSON && tc.name == "float" {
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
						hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format),
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
					for ri, value := range tc.values {
						items := hff.List(value, timestamp)
						match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": value, "items": items}))
						if err != nil {
							t.Fatal(source, ri, err)
						}
						expected = append(expected, match)
						labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, ri))
						itemsJSON := "[" + tc.json[ri] + "," + timestampJSON + "]"
						values = append(values, "("+sqlFixtureString(tc.json[ri])+"::"+string(format)+","+sqlFixtureString(itemsJSON)+"::"+string(format)+")")
					}
					params, args := "", ""
					if len(parameterTypes) > 0 {
						params = "(" + strings.Join(parameterTypes, ",") + ")"
						args = "(" + strings.Join(arguments, ",") + ")"
					}
					fmt.Fprintf(&script, "PREPARE json_disjoint_%d_%d%s AS SELECT %s FROM (VALUES %s) records(value,items); EXECUTE json_disjoint_%d_%d%s;\n", fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
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
		})
	}
}

func TestPostgreSQLJSONTimestampMixedNumericUnionComparison(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("left", schema.Union(schema.TimestampType, schema.IntType)),
		schema.Field("right", schema.Union(schema.TimestampType, schema.FloatType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	stamp := hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC))
	rows := []struct {
		left, right         hff.Value
		leftJSON, rightJSON string
	}{
		{hff.Int(9007199254740993), hff.Float(9007199254740992), "9007199254740993", "9007199254740992.0"},
		{hff.Int(7), hff.Float(7.0), "7", "7.0"},
		{stamp, stamp, `"2026-01-01T00:00:00.000000123Z"`, `"2026-01-01T00:00:00.000000123Z"`},
		{hff.Int(1), stamp, "1", `"2026-01-01T00:00:00.000000123Z"`},
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for si, source := range []string{
			`left=right`, `left!=right`, `left>right`, `left<right`,
			`right=left`, `right>left`, `right<left`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "left_value"), format),
				hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "right_value"), format),
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
				match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": row.left, "right": row.right}))
				if err != nil {
					t.Fatal(source, ri, err)
				}
				expected = append(expected, match)
				labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, ri))
				values = append(values, "("+sqlFixtureString(row.leftJSON)+"::"+string(format)+","+sqlFixtureString(row.rightJSON)+"::"+string(format)+")")
			}
			params, args := "", ""
			if len(parameterTypes) > 0 {
				params = "(" + strings.Join(parameterTypes, ",") + ")"
				args = "(" + strings.Join(arguments, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE json_mixed_numeric_%d_%d%s AS SELECT %s FROM (VALUES %s) records(left_value,right_value); EXECUTE json_mixed_numeric_%d_%d%s;\n", fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
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
