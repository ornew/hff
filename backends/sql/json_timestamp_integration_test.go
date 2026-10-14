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

func TestPostgreSQLJSONTimestampMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Timestamp("created")),
		schema.List("items", schema.NullableType(schema.TimestampType)),
		schema.List("sorted", schema.TimestampType),
		schema.Any("raw"),
		schema.Field("payload", schema.Struct(schema.Nullable(schema.Timestamp("created")))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		value   hff.Value
		json    string
		payload string
	}
	fixtures := []fixture{
		{hff.Null(), "null", `{"created":null}`},
		{hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), `"0000-01-01T00:00:00Z"`, `{"created":"0000-01-01T00:00:00Z"}`},
		{hff.Timestamp(time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC)), `"1969-12-31T23:59:59.999999999Z"`, `{"created":"1969-12-31T23:59:59.999999999Z"}`},
		{hff.Timestamp(time.Date(1970, 1, 1, 0, 0, 0, 1, time.UTC)), `"1970-01-01T00:00:00.000000001Z"`, `{"created":"1970-01-01T00:00:00.000000001Z"}`},
		{hff.Timestamp(time.Date(2026, 10, 1, 0, 0, 0, 123, time.UTC)), `"2026-10-01T00:00:00.000000123Z"`, `{"created":"2026-10-01T00:00:00.000000123Z"}`},
		{hff.Timestamp(time.Date(2026, 10, 1, 9, 0, 0, 123, time.FixedZone("JST", 9*60*60))), `"2026-10-01T00:00:00.000000123Z"`, `{"created":"2026-10-01T00:00:00.000000123Z"}`},
		{hff.Timestamp(time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)), `"9999-12-31T23:59:59.999999999Z"`, `{"created":"9999-12-31T23:59:59.999999999Z"}`},
	}
	sources := []string{
		`created=null`, `created`, `created:type="timestamp"`, `created:exists`,
		`created:[timestamp]:exists`, `created:[int]:exists`,
		`created="0000-01-01T00:00:00Z":timestamp`,
		`created:before("1970-01-01T00:00:00Z":timestamp)`,
		`created:after("1969-12-31T23:59:59.999999998Z":timestamp)`,
		`created:in("0000-01-01T00:00:00Z":timestamp,"2026-10-01T00:00:00.000000123Z":timestamp)`,
		`created:year=0`, `created:month=10`, `created:dayofyear=274`,
		`created:milliseconds=999`, `created:seconds=59`,
		`created:string="1969-12-31T23:59:59.999999999Z"`,
		`created:json="\"2026-10-01T00:00:00.000000123Z\""`,
		`created:add("1ns":duration)="1970-01-01T00:00:00Z":timestamp`,
		`created:add("1ns":duration)=null`,
		`payload.created:year=2026`, `payload.created=null`,
		`items[0]:year=2026`, `items[0]:exists`, `items=items`, `items:contains(null)`,
		`items:contains("2026-10-01T00:00:00.000000123Z":timestamp)`,
		`items:unique`, `items:json="[null]"`, `[created]:json="[null]"`,
		`items:map(x=>x):json="[null]"`,
		`items:map(x=>x:add("1ns":duration)):json="[\"0000-01-01T00:00:00.000000001Z\"]"`,
		`items:map(x=>x:add("1ns":duration)):json="[null]"`,
		`items:map(x=>x:add("1ns":duration)):contains("0000-01-01T00:00:00.000000001Z":timestamp)`,
		`items:flatmap(x=>[x:add("1ns":duration)]):json="[null]"`,
		`items:concat([null]):map(x=>x:add("1ns":duration)):json:contains("null")`,
		`items:flatmap(x=>[x]):json="[null]"`,
		`items:foldl("2026-01-01T00:00:00Z":timestamp,(a,x)=>a):exists`,
		`items:foldr("2026-01-01T00:00:00Z":timestamp,(x,a)=>a):string="2026-01-01T00:00:00Z"`,
		`sorted:sort[0]:year=0`, `sorted:sort[1]:year=2026`,
		`sorted:unique`, `payload:json="{\"created\":null}"`,
		`sorted:json="[\"2026-01-01T00:00:00.000000123Z\",\"0000-01-01T00:00:00Z\"]"`,
		`raw:type="string"`, `raw:[string]:exists`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		bindings := []hffsql.Binding{
			hffsql.BindJSON(hff.Path{"created"}, hffsql.Column("records", "created"), format),
			hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format),
			hffsql.BindJSON(hff.Path{"sorted"}, hffsql.Column("records", "sorted"), format),
			hffsql.BindJSON(hff.Path{"raw"}, hffsql.Column("records", "raw"), format),
			hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "payload"), format),
		}
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
			var parameterTypes, arguments []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				parameterTypes = append(parameterTypes, typ)
				arguments = append(arguments, literal)
			}
			var rows []string
			for ri, row := range fixtures {
				itemValues := []hff.Value{row.value}
				itemsJSON := "[" + row.json + "]"
				if ri == 0 {
					itemValues = nil
					itemsJSON = "[]"
				}
				sortedValues := []hff.Value{
					hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)),
					hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)),
				}
				sortedJSON := `["2026-01-01T00:00:00.000000123Z","0000-01-01T00:00:00Z"]`
				if ri == 0 {
					sortedValues[1] = sortedValues[0]
					sortedJSON = `["2026-01-01T00:00:00.000000123Z","2026-01-01T00:00:00.000000123Z"]`
				}
				record := hff.Object(map[string]hff.Value{
					"created": row.value,
					"items":   hff.List(itemValues...),
					"sorted":  hff.List(sortedValues...),
					"raw":     hff.String("2026-10-01T00:00:00.000000123Z"),
					"payload": hff.Object(map[string]hff.Value{"created": row.value}),
				})
				match, err := filter.Match(ctx, record)
				if err != nil {
					t.Fatal(source, ri, err)
				}
				expected = append(expected, match)
				labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, ri))
				rows = append(rows, "("+sqlFixtureString(row.json)+"::"+string(format)+","+sqlFixtureString(itemsJSON)+"::"+string(format)+","+sqlFixtureString(sortedJSON)+"::"+string(format)+","+sqlFixtureString(`"2026-10-01T00:00:00.000000123Z"`)+"::"+string(format)+","+sqlFixtureString(row.payload)+"::"+string(format)+")")
			}
			params, args := "", ""
			if len(parameterTypes) > 0 {
				params = "(" + strings.Join(parameterTypes, ",") + ")"
				args = "(" + strings.Join(arguments, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE json_timestamp_%d_%d%s AS SELECT %s FROM (VALUES %s) records(created,items,sorted,raw,payload); EXECUTE json_timestamp_%d_%d%s;\n", fi, si, params, query.Where(), strings.Join(rows, ","), fi, si, args)
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

func TestPostgreSQLJSONTimestampIntUnionMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.TimestampType, schema.IntType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value hff.Value
		json  string
	}{
		{hff.Null(), "null"},
		{hff.Int(0), "0"},
		{hff.Int(2026), "2026"},
		{hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), `"0000-01-01T00:00:00Z"`},
		{hff.Timestamp(time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC)), `"1969-12-31T23:59:59.999999999Z"`},
		{hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)), `"2026-01-01T00:00:00.000000123Z"`},
	}
	sources := []string{
		`value:type="timestamp"`, `value:type="int"`,
		`value:[timestamp]:year=0`, `value:[timestamp]:milliseconds=0`,
		`value:before("1970-01-01T00:00:00Z":timestamp)`,
		`value="2026-01-01T00:00:00.000000123Z":timestamp`,
		`value!=2026`, `value=value`,
		`value:in("2026-01-01T00:00:00.000000123Z":timestamp,2026)`,
		`value:string="2026-01-01T00:00:00.000000123Z"`,
		`value:json="\"2026-01-01T00:00:00.000000123Z\""`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for si, source := range sources {
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
			fmt.Fprintf(&script, "PREPARE json_timestamp_union_%d_%d%s AS SELECT %s FROM (VALUES %s) records(value); EXECUTE json_timestamp_union_%d_%d%s;\n", fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
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
