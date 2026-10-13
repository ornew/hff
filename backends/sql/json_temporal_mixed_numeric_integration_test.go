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

func TestPostgreSQLJSONTemporalMixedNumericUnionMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	timestamp := hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC))
	type row struct {
		value hff.Value
		json  string
		meta  string
	}
	for _, tc := range []struct {
		name   string
		typeOf hff.Type
		rows   []row
	}{
		{
			name:   "timestamp",
			typeOf: schema.Union(schema.TimestampType, schema.IntType, schema.FloatType, schema.NullType),
			rows: []row{
				{timestamp, `"2026-01-01T00:00:00.000000123Z"`, `{"version":1,"types":{}}`},
				{hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), `"0000-01-01T00:00:00Z"`, `{"version":1,"types":{}}`},
				{hff.Timestamp(time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)), `"9999-12-31T23:59:59.999999999Z"`, `{"version":1,"types":{}}`},
				{hff.Int(9007199254740993), `9007199254740993`, `{"version":1,"types":{"":{"type":"int"}}}`},
				{hff.Float(9007199254740992), `9007199254740992.0`, `{"version":1,"types":{"":{"type":"float","text":"9007199254740992.0"}}}`},
				{hff.Float(math.Copysign(0, -1)), `-0.0`, `{"version":1,"types":{"":{"type":"float","text":"-0.0"}}}`},
				{hff.Null(), `null`, `{"version":1,"types":{}}`},
			},
		},
		{
			name:   "duration",
			typeOf: schema.Union(schema.DurationType, schema.IntType, schema.FloatType, schema.NullType),
			rows: []row{
				{hff.Duration(time.Hour), `"1h"`, `{"version":1,"types":{}}`},
				{hff.Int(9007199254740993), `9007199254740993`, `{"version":1,"types":{"":{"type":"int"}}}`},
				{hff.Float(9007199254740992), `9007199254740992.0`, `{"version":1,"types":{"":{"type":"float","text":"9007199254740992.0"}}}`},
				{hff.Float(math.Copysign(0, -1)), `-0.0`, `{"version":1,"types":{"":{"type":"float","text":"-0.0"}}}`},
				{hff.Null(), `null`, `{"version":1,"types":{}}`},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taggedJSON := `"1h"`
			taggedValue := hff.Duration(time.Hour)
			if tc.name == "timestamp" {
				taggedJSON = `"2026-01-01T00:00:00.000000123Z"`
				taggedValue = timestamp
			}
			e, err := engine.New(engine.WithSchema(schema.Struct(
				schema.Field("value", tc.typeOf),
				schema.List("items", tc.typeOf),
			)), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			sources := []string{
				`value:type="number"`, `value:[int]>0`, `value:[float]>0.5`,
				`value=value`, `value:in(9007199254740992.0,9007199254740993)`,
				`value:in(null,9007199254740993)`, `value:string="-0.0"`,
				`value:json="-0.0"`, `items:contains(9007199254740993)`, `items:unique`,
			}
			if tc.name == "timestamp" {
				sources = append(sources, `value:[timestamp]:year=2026`, `value:in("2026-01-01T00:00:00.000000123Z":timestamp,1)`)
			} else {
				sources = append(sources, `value:[duration]:hours=1`, `value:in("1h":duration,1)`)
			}
			for _, mode := range []struct {
				name   string
				format hffsql.JSONFormat
				meta   bool
			}{{"json", hffsql.JSON, false}, {"jsonb-metadata", hffsql.JSONB, true}} {
				t.Run(mode.name, func(t *testing.T) {
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
						bindingOpts := []hffsql.JSONBindingOption{}
						itemBindingOpts := []hffsql.JSONBindingOption{}
						if mode.meta {
							bindingOpts = append(bindingOpts, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
							itemBindingOpts = append(itemBindingOpts, hffsql.WithTypeMetadata(hffsql.Column("records", "itemmeta")))
						}
						query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
							hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), mode.format, bindingOpts...),
							hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), mode.format, itemBindingOpts...),
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
						for ri, row := range tc.rows {
							items := hff.List(row.value, taggedValue)
							match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": row.value, "items": items}))
							if err != nil {
								t.Fatal(source, ri, err)
							}
							expected = append(expected, match)
							labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
							itemsJSON := "[" + row.json + "," + taggedJSON + "]"
							itemsMeta := strings.Replace(row.meta, `"":`, `"/0":`, 1)
							values = append(values, "("+sqlFixtureString(row.json)+"::"+string(mode.format)+","+sqlFixtureString(row.meta)+"::jsonb,"+
								sqlFixtureString(itemsJSON)+"::"+string(mode.format)+","+sqlFixtureString(itemsMeta)+"::jsonb)")
						}
						params, args := "", ""
						if len(parameterTypes) > 0 {
							params = "(" + strings.Join(parameterTypes, ",") + ")"
							args = "(" + strings.Join(arguments, ",") + ")"
						}
						fmt.Fprintf(&script, "PREPARE json_temporal_mixed_%d%s AS SELECT %s FROM (VALUES %s) records(value,meta,items,itemmeta); EXECUTE json_temporal_mixed_%d%s;\n", si, params, query.Where(), strings.Join(values, ","), si, args)
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
		})
	}
}

func TestPostgreSQLJSONTemporalAsymmetricNumericTypesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("mixed", schema.Union(schema.TimestampType, schema.IntType, schema.FloatType)),
		schema.Field("known", schema.Union(schema.TimestampType, schema.FloatType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		mixed, known         hff.Value
		mixedJSON, knownJSON string
	}{
		{hff.Int(9007199254740993), hff.Float(9007199254740992), `9007199254740993`, `9007199254740993`},
		{hff.Int(math.MaxInt64), hff.Float(9223372036854775808), `9223372036854775807`, `9223372036854775808`},
		{hff.Float(7), hff.Float(7), `7.0`, `7`},
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for i, source := range []string{`mixed=known`, `mixed!=known`, `mixed>known`, `mixed<known`, `known=mixed`, `known>mixed`, `known<mixed`, `mixed:in(known)`, `known:in(mixed)`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"mixed"}, hffsql.Column("records", "mixed"), hffsql.JSON),
			hffsql.BindJSON(hff.Path{"known"}, hffsql.Column("records", "known"), hffsql.JSONB),
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
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"mixed": row.mixed, "known": row.known}))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, match)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			values = append(values, "("+sqlFixtureString(row.mixedJSON)+"::json,"+sqlFixtureString(row.knownJSON)+"::jsonb)")
		}
		params, args := "", ""
		if len(parameterTypes) > 0 {
			params = "(" + strings.Join(parameterTypes, ",") + ")"
			args = "(" + strings.Join(arguments, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE json_temporal_asymmetric_%d%s AS SELECT %s FROM (VALUES %s) records(mixed,known); EXECUTE json_temporal_asymmetric_%d%s;\n", i, params, query.Where(), strings.Join(values, ","), i, args)
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
