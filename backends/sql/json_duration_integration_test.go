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

func TestPostgreSQLJSONDurationMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Duration("elapsed")),
		schema.List("items", schema.NullableType(schema.DurationType)),
		schema.Field("payload", schema.Struct(schema.Duration("elapsed"))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		record              hff.Value
		elapsed, items, obj string
	}
	fixtures := []fixture{
		{hff.Object(map[string]hff.Value{"elapsed": hff.Null(), "items": hff.List(), "payload": hff.Object(map[string]hff.Value{"elapsed": hff.Duration(0)})}), "null", "[]", `{"elapsed":"0s"}`},
		{hff.Object(map[string]hff.Value{"elapsed": hff.Duration(0), "items": hff.List(hff.Duration(time.Second), hff.Null()), "payload": hff.Object(map[string]hff.Value{"elapsed": hff.Duration(time.Nanosecond)})}), `"0s"`, `["1s",null]`, `{"elapsed":"0.000000001s"}`},
		{hff.Object(map[string]hff.Value{"elapsed": hff.Duration(time.Second + time.Nanosecond), "items": hff.List(hff.Duration(time.Second), hff.Duration(-time.Second)), "payload": hff.Object(map[string]hff.Value{"elapsed": hff.Duration(-time.Second)})}), `"1.000000001s"`, `["1s","-1s"]`, `{"elapsed":"-1s"}`},
	}
	sources := []string{
		`elapsed=null`, `elapsed:nanoseconds=0`, `elapsed:nanoseconds=1000000001`,
		`elapsed:string="1.000000001s"`, `elapsed:json="\"0s\""`,
		`elapsed="1.000000001s":duration`, `elapsed:type="duration"`,
		`items:contains("1s":duration)`, `items:contains(null)`, `items=["1s":duration,null]`, `items:sort=null`,
		`items:map(x=>x:string):size=items:size`,
		`items:foldl("0s":duration,(acc,x)=>acc):nanoseconds=0`,
		`payload.elapsed:nanoseconds=1`, `payload={"elapsed":"1ns":duration}`, `[elapsed]:json="[null]"`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		bindings := []hffsql.Binding{
			hffsql.BindJSON(hff.Path{"elapsed"}, hffsql.Column("records", "elapsed"), format),
			hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format),
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
				match, err := filter.Match(ctx, row.record)
				if err != nil {
					t.Fatal(source, ri, err)
				}
				rows = append(rows, "("+sqlFixtureString(row.elapsed)+"::"+string(format)+","+sqlFixtureString(row.items)+"::"+string(format)+","+sqlFixtureString(row.obj)+"::"+string(format)+")")
				expected = append(expected, match)
				labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, ri))
			}
			params, args := "", ""
			if len(parameterTypes) > 0 {
				params = "(" + strings.Join(parameterTypes, ",") + ")"
				args = "(" + strings.Join(arguments, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE json_duration_%d_%d%s AS SELECT %s FROM (VALUES %s) records(elapsed,items,payload); EXECUTE json_duration_%d_%d%s;\n", fi, si, params, query.Where(), strings.Join(rows, ","), fi, si, args)
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
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}

func TestPostgreSQLJSONDurationIntUnionMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.DurationType, schema.IntType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value hff.Value
		json  string
	}{
		{hff.Null(), "null"},
		{hff.Duration(0), `"0s"`},
		{hff.Duration(time.Nanosecond), `"0.000000001s"`},
		{hff.Duration(-time.Nanosecond), `"-0.000000001s"`},
		{hff.Duration(time.Duration(-1 << 63)), `"-2562047h47m16.854775808s"`},
		{hff.Duration(time.Duration(1<<63 - 1)), `"2562047h47m16.854775807s"`},
		{hff.Int(0), "0"},
		{hff.Int(1), "1"},
		{hff.Int(-1), "-1"},
		{hff.Int(-1 << 63), "-9223372036854775808"},
		{hff.Int(1<<63 - 1), "9223372036854775807"},
	}
	sources := []string{
		`value`, `!value`, `value:type="duration"`,
		`value:[duration]:nanoseconds=1`, `value:[int]=1`,
		`value="1ns":duration`, `value!="1ns":duration`,
		`value=1`, `value>0`, `value=value`, `value!=value`,
		`value:in("1ns":duration,1)`, `value:in(value)`,
		`value:nanoseconds=1`, `value:seconds=0.000000001`,
		`value:string="0.000000001s"`, `value:string="-0.000000001s"`,
		`value:json="\"0.000000001s\""`, `value:json="\"-0.000000001s\""`,
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
			fmt.Fprintf(&script, "PREPARE json_duration_union_%d_%d%s AS SELECT %s FROM (VALUES %s) records(value); EXECUTE json_duration_union_%d_%d%s;\n",
				fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
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

func TestPostgreSQLJSONDurationIntListMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.List("items", schema.Union(schema.DurationType, schema.IntType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value hff.Value
		json  string
	}{
		{hff.List(), `[]`},
		{hff.List(hff.Duration(time.Nanosecond), hff.Duration(0)), `["0.000000001s","0s"]`},
		{hff.List(hff.Int(1), hff.Int(-1)), `[1,-1]`},
		{hff.List(hff.Duration(time.Nanosecond), hff.Int(1)), `["0.000000001s",1]`},
		{hff.List(hff.Int(1), hff.Int(1)), `[1,1]`},
		{hff.List(hff.Duration(time.Duration(-1<<63)), hff.Duration(time.Duration(1<<63-1))), `["-2562047h47m16.854775808s","2562047h47m16.854775807s"]`},
		{hff.List(hff.Int(-1<<63), hff.Int(1<<63-1)), `[-9223372036854775808,9223372036854775807]`},
		{hff.List(hff.Null()), `[null]`},
	}
	sources := []string{
		`items:contains("1ns":duration)`, `items:contains(1)`,
		`items[0]=items[1]`, `items[0]!=items[1]`, `items[0]<items[1]`,
		`items[0]:[duration]<items[1]:[duration]`,
		`"1ns":duration:in(items[0]:[duration])`,
		`items:unique`, `items:sort:json="[-1,1]"`,
		`items:sort:json="[\"0s\",\"0.000000001s\"]"`,
		`items:all(x=>x)`, `items:map(x=>x:type):contains("duration")`,
		`items:foldl(0,(acc,x)=>acc)=0`,
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
			for ri, row := range rows {
				match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": row.value}))
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
			fmt.Fprintf(&script, "PREPARE json_duration_list_%d_%d%s AS SELECT %s FROM (VALUES %s) records(items); EXECUTE json_duration_list_%d_%d%s;\n",
				fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
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

func TestPostgreSQLJSONDurationBoolUnionMatchesLocal(t *testing.T) {
	runPostgreSQLJSONDurationScalarUnionMatchesLocal(t, false)
}

func TestPostgreSQLJSONDurationIntBoolUnionMatchesLocal(t *testing.T) {
	runPostgreSQLJSONDurationScalarUnionMatchesLocal(t, true)
}

func runPostgreSQLJSONDurationScalarUnionMatchesLocal(t *testing.T, withInt bool) {
	t.Helper()
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	members := []hff.Type{schema.DurationType, schema.BoolType, schema.NullType}
	if withInt {
		members = append(members, schema.IntType)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(members...)),
		schema.List("items", schema.Union(members...)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		record hff.Value
		value  string
		items  string
	}
	rows := []fixture{
		{hff.Object(map[string]hff.Value{"value": hff.Null(), "items": hff.List()}), "null", `[]`},
		{hff.Object(map[string]hff.Value{"value": hff.Duration(0), "items": hff.List(hff.Duration(0), hff.Bool(false))}), `"0s"`, `["0s",false]`},
		{hff.Object(map[string]hff.Value{"value": hff.Duration(time.Nanosecond), "items": hff.List(hff.Duration(time.Nanosecond), hff.Bool(true))}), `"0.000000001s"`, `["0.000000001s",true]`},
		{hff.Object(map[string]hff.Value{"value": hff.Duration(time.Nanosecond), "items": hff.List(hff.Duration(0), hff.Duration(time.Nanosecond))}), `"0.000000001s"`, `["0s","0.000000001s"]`},
		{hff.Object(map[string]hff.Value{"value": hff.Bool(false), "items": hff.List(hff.Bool(false), hff.Bool(false))}), "false", `[false,false]`},
		{hff.Object(map[string]hff.Value{"value": hff.Bool(true), "items": hff.List(hff.Bool(true), hff.Null())}), "true", `[true,null]`},
	}
	if withInt {
		rows = append(rows, fixture{hff.Object(map[string]hff.Value{"value": hff.Int(1), "items": hff.List(hff.Int(1), hff.Bool(true))}), "1", `[1,true]`})
	}
	sources := []string{
		`value`, `!value`, `value:type="duration"`, `value:type="bool"`,
		`value:[bool]:not`, `value="1ns":duration`, `value=true`,
		`value=value`, `value!=value`, `value:in(true,"1ns":duration)`,
		`"1ns":duration:in(value)`, `items:contains(true)`, `items:unique`,
		`items[0]<items[1]`, `items[0]:[duration]<items[1]:[duration]`,
		`"1ns":duration:in(items[0]:[duration])`,
		`value:and(items[0]:[bool])`, `value:or(items[0]:[bool])`,
		`items:all(x=>x)`, `items:map(x=>x:type):contains("bool")`,
		`items:map(x=>x):json="[\"0.000000001s\",true]"`,
		`items:map(x=>x):json="[1,true]"`,
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
			for ri, row := range rows {
				match, err := filter.Match(ctx, row.record)
				if err != nil {
					t.Fatal(source, ri, err)
				}
				expected = append(expected, match)
				labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, ri))
				values = append(values, "("+sqlFixtureString(row.value)+"::"+string(format)+","+sqlFixtureString(row.items)+"::"+string(format)+")")
			}
			params, args := "", ""
			if len(parameterTypes) > 0 {
				params = "(" + strings.Join(parameterTypes, ",") + ")"
				args = "(" + strings.Join(arguments, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE json_duration_bool_%d_%d%s AS SELECT %s FROM (VALUES %s) records(value,items); EXECUTE json_duration_bool_%d_%d%s;\n",
				fi, si, params, query.Where(), strings.Join(values, ","), fi, si, args)
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
