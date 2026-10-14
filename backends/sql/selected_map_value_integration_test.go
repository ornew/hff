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

func TestPostgreSQLSelectedMapValueMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fixtures := []struct {
		name         string
		typ          hff.Type
		value, other hff.Value
	}{
		{"Int", schema.IntType, hff.Int(math.MaxInt64), hff.Int(math.MinInt64)},
		{"Float", schema.FloatType, hff.Float(1), hff.Float(math.Copysign(0, -1))},
		{"String", schema.StringType, hff.String("alpha"), hff.String("")},
		{"Bool", schema.BoolType, hff.Bool(true), hff.Bool(false)},
		{"Bytes", schema.BytesType, hff.Bytes([]byte{0, 254}), hff.Bytes(nil)},
		{"Duration", schema.DurationType, hff.Duration(2*time.Hour + 123*time.Nanosecond), hff.Duration(-time.Nanosecond)},
		{"Timestamp", schema.TimestampType, hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 123, time.UTC)), hff.Timestamp(time.Unix(0, 999).UTC())},
		{"NumericUnion", schema.Union(schema.IntType, schema.FloatType), hff.Int(9007199254740993), hff.Float(math.SmallestNonzeroFloat64)},
		{"List", schema.ListOf(schema.Union(schema.IntType, schema.FloatType, schema.NullType)), hff.List(hff.Int(1), hff.Float(1), hff.Null()), hff.List()},
		{"Map", schema.MapOf(schema.NullableType(schema.StringType)), hff.Object(map[string]hff.Value{"label": hff.String("auth"), "empty": hff.String("")}), hff.Object(map[string]hff.Value{"nil": hff.Null()})},
		{"Struct", schema.Struct(schema.Int("i"), schema.Nullable(schema.Float("f"))), hff.Object(map[string]hff.Value{"i": hff.Int(1), "f": hff.Float(1)}), hff.Object(map[string]hff.Value{"i": hff.Int(2), "f": hff.Null()})},
		{"Any", schema.AnyType, hff.List(hff.Int(1), hff.Float(1)), hff.Object(map[string]hff.Value{"nested": hff.Float(1)})},
		{"MapUnionInt", schema.IntType, hff.Int(1), hff.Int(2)},
		{"MapUnionFloat", schema.FloatType, hff.Float(1), hff.Float(math.Copysign(0, -1))},
	}
	sources := []string{`data:get(key):exists`, `data:get(key)=null`, `!data:get(key)`, `data:get(key!)=null`, `!(data:get(key!)=null)`, `data!:get(key)=null`, `data:get(guard!.name)=null`, `!(data:get(guard!.name)=null)`, `data:get("target"):exists`, `data.target:exists`, `data["target"]:exists`, `data:get(key)=data:get("target")`, `groups:any(g=>g:get(key):exists)`, `groups:any(g=>keys:any(key=>g:get(key):exists))`, `keys:any(key=>data:get(key):exists)`}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, fixture := range fixtures {
		typ := schema.MapOf(schema.NullableType(fixture.typ))
		if strings.HasPrefix(fixture.name, "MapUnion") {
			typ = schema.Union(schema.MapOf(schema.NullableType(schema.IntType)), schema.MapOf(schema.NullableType(schema.FloatType)))
		}
		e := selectedMapEngine(t, typ)
		populated := hff.Object(map[string]hff.Value{"target": fixture.value, "a/b": fixture.other, "~": fixture.other, "": fixture.value, "東京": fixture.value, "null": hff.Null()})
		dataValues := []hff.Value{populated, hff.Object(nil), hff.String("inapplicable"), hff.Null()}
		keyValues := []hff.Value{hff.String("target"), hff.String("a/b"), hff.String("~"), hff.String(""), hff.String("東京"), hff.String("null"), hff.String("missing"), hff.Null(), hff.Int(1)}
		var records []hff.Value
		for _, data := range dataValues {
			for _, key := range keyValues {
				for _, guard := range []hff.Value{hff.Null(), hff.Object(map[string]hff.Value{"name": hff.String("target")})} {
					records = append(records, hff.Object(map[string]hff.Value{"data": data, "key": key, "guard": guard, "groups": hff.List(data, populated), "keys": hff.List(key, hff.String("target"), hff.Null()), "name": hff.String("x")}))
				}
			}
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			fields := []string{"data", "key", "guard", "groups", "keys"}
			var rows []string
			var columns []string
			columns = append(columns, fields...)
			for _, field := range fields {
				columns = append(columns, field+"_meta")
			}
			columns = append(columns, "name")
			for ri, record := range records {
				values, _ := record.AsObject()
				var row []string
				for _, field := range fields {
					literal := sqlFixtureString(selectedMapJSONFixture(t, values[field]))
					if values[field].Kind() == hff.NullKind && ri%2 == 0 {
						literal = "NULL"
					}
					row = append(row, literal+"::"+string(format))
				}
				for _, field := range fields {
					row = append(row, sqlFixtureString(pairTypeMetadata(t, values[field]))+"::jsonb")
				}
				row = append(row, "'x'::text")
				rows = append(rows, "("+strings.Join(row, ",")+")")
			}
			for _, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(fixture.name, source, err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, selectedMapOptions(format, format == hffsql.JSONB)...)
				if err != nil {
					t.Fatal(fixture.name, source, err)
				}
				var types, args []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					args = append(args, literal)
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				index := len(expected)
				fmt.Fprintf(&script, "PREPARE selected_%d%s AS SELECT %s FROM (VALUES %s) records(%s); EXECUTE selected_%d%s;\n", index, parameters, q.Where(), strings.Join(rows, ","), strings.Join(columns, ","), index, arguments)
				for ri, record := range records {
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(fixture.name, source, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s %s %s row%d", fixture.name, format, source, ri))
				}
			}
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
		t.Fatalf("got%d results,want%d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d selected-value results", len(expected))
}

// Temporal values use their declared JSON string representation; the numeric
// fixture serializer keeps Int/Float spellings and signed zero unchanged.
func selectedMapJSONFixture(t testing.TB, value hff.Value) string {
	var storage func(hff.Value) hff.Value
	storage = func(value hff.Value) hff.Value {
		switch value.Kind() {
		case hff.DurationKind:
			d, _ := value.AsDuration()
			return hff.String(d.String())
		case hff.TimestampKind:
			d, _ := value.AsTimestamp()
			return hff.String(d.UTC().Format(time.RFC3339Nano))
		case hff.ListKind:
			items, _ := value.AsList()
			for i, item := range items {
				items[i] = storage(item)
			}
			return hff.List(items...)
		case hff.ObjectKind:
			fields, _ := value.AsObject()
			for key, item := range fields {
				fields[key] = storage(item)
			}
			return hff.Object(fields)
		}
		return value
	}
	return metadataJSONFixture(t, storage(value))
}

func TestPostgreSQLSelectedMapValuePreservesCarrier(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, d := foldedNullExtension(t)
	cases := []struct {
		typ          hff.Type
		carrier, raw string
	}{
		{schema.IntType, "bigint", `{"target":1,"null":null}`},
		{schema.FloatType, "double precision", `{"target":1.0,"null":null}`},
		{schema.StringType, "text", `{"target":"x","null":null}`},
		{schema.BoolType, "boolean", `{"target":true,"null":null}`},
		{schema.BytesType, "bytea", `{"target":"AA==","null":null}`},
		{schema.DurationType, "bigint", `{"target":"1ns","null":null}`},
		{schema.TimestampType, "numeric", `{"target":"0000-01-01T00:00:00.000000001Z","null":null}`},
	}
	var script strings.Builder
	var labels []string
	for _, tc := range cases {
		base := selectedMapEngine(t, schema.MapOf(schema.NullableType(tc.typ)))
		e, err := engine.New(engine.WithSchema(base.Schema()), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`name:nullcarrier(data:get(key))`, `name:nullcarrier(data:get("target"))`} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			options := selectedMapOptions(hffsql.JSON, false)
			options = append(options, hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
				return c.Expression(schema.BoolType, "pg_typeof({})::text='"+tc.carrier+"'", args[0])
			})))
			q, err := hffsql.Build(ctx, p, options...)
			if err != nil {
				t.Fatal(err)
			}
			var types, args []string
			for _, arg := range q.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, literal)
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			index := len(labels)
			rows := []string{}
			for _, data := range []string{"NULL", `'null'`, sqlFixtureString(tc.raw)} {
				for _, key := range []string{`'"target"'`, `'"missing"'`, `'"null"'`, `'null'`} {
					rows = append(rows, "("+data+"::json,"+key+"::json,'x'::text)")
					labels = append(labels, tc.carrier+" "+source)
				}
			}
			fmt.Fprintf(&script, "PREPARE mapcarrier_%d%s AS SELECT %s FROM (VALUES %s) records(data,key,name); EXECUTE mapcarrier_%d%s;\n", index, parameters, q.Where(), strings.Join(rows, ","), index, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(labels) {
		t.Fatalf("got%d results,want%d: %s", len(results), len(labels), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s lost carrier: %s", labels[i], result)
		}
	}
	t.Logf("checked %d selected-value carrier observations", len(labels))
}

func TestPostgreSQLSelectedMapFloatJSONBWithoutMetadata(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e := selectedMapEngine(t, schema.MapOf(schema.FloatType))
	var script strings.Builder
	sources := []string{`data:get(key):[float]:exists`, `!data:get(key):[int]:exists`, `data:get(key)=1.0`, `data:get("target"):[float]=1.0`, `data.target:[float]=1.0`, `data["target"]:[float]=1.0`}
	for i, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, selectedMapOptions(hffsql.JSONB, false)...)
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE rawfloat_%d%s AS SELECT %s FROM (VALUES ('{\"target\":1}'::jsonb,'\"target\"'::jsonb)) records(data,key); EXECUTE rawfloat_%d%s;\n", i, parameters, q.Where(), i, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(sources) {
		t.Fatalf("got%d results,want%d: %s", len(results), len(sources), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s lost schema-known Float: %s", sources[i], result)
		}
	}
}

func TestPostgreSQLSelectedKnownObjectPresence(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sources := []string{`data:get(key)=null`, `data:get(key!)=null`, `!(data:get(key!)=null)`, `data!:get(key!)=null`, `data:get(guard!.name)=null`, `data:get("target")=null`, `data:[object]:get(key!)=null`, `!(data:[object]:get(key!)=null)`}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, typ := range []hff.Type{schema.MapOf(schema.NullableType(schema.FloatType)), schema.Struct(schema.Optional(schema.Nullable(schema.Float("target"))))} {
		e := selectedObjectEngine(t, schema.NullableType(typ))
		var records []hff.Value
		for _, data := range []hff.Value{hff.Null(), hff.Object(nil), hff.Object(map[string]hff.Value{"target": hff.Null()}), hff.Object(map[string]hff.Value{"target": hff.Float(1)})} {
			for _, key := range []hff.Value{hff.String("target"), hff.String("missing"), hff.Null(), hff.Int(1)} {
				for _, guard := range []hff.Value{hff.Null(), hff.Object(map[string]hff.Value{"name": hff.String("target")})} {
					records = append(records, hff.Object(map[string]hff.Value{"data": data, "key": key, "guard": guard, "groups": hff.List(), "keys": hff.List(), "name": hff.String("x")}))
				}
			}
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var rows []string
			for ri, record := range records {
				fields, _ := record.AsObject()
				var row []string
				for _, field := range []string{"data", "key", "guard"} {
					literal := sqlFixtureString(selectedMapJSONFixture(t, fields[field]))
					if fields[field].Kind() == hff.NullKind && ri%2 == 0 {
						literal = "NULL"
					}
					row = append(row, literal+"::"+string(format))
				}
				for _, field := range []string{"data", "key", "guard"} {
					row = append(row, sqlFixtureString(pairTypeMetadata(t, fields[field]))+"::jsonb")
				}
				rows = append(rows, "("+strings.Join(row, ",")+")")
			}
			for _, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, selectedMapOptions(format, format == hffsql.JSONB)...)
				if err != nil {
					t.Fatal(err)
				}
				var types, args []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					args = append(args, literal)
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				index := len(expected)
				fmt.Fprintf(&script, "PREPARE known_%d%s AS SELECT %s FROM (VALUES %s) records(data,key,guard,data_meta,key_meta,guard_meta); EXECUTE known_%d%s;\n", index, parameters, q.Where(), strings.Join(rows, ","), index, arguments)
				for ri, record := range records {
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(source, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%v %s %s row%d", typ, format, source, ri))
				}
			}
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
		t.Fatalf("got%d results,want%d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d known-object presence results", len(expected))
}
