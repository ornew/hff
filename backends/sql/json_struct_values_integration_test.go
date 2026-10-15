//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
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

func TestPostgreSQLStructValuesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var records []hff.Value
	seen := map[string]bool{}
	for _, record := range dynamicStructRecords() {
		data, _ := record.Lookup("data")
		key := taggedCollectionJSON(t, data)
		if !seen[key] {
			records = append(records, record)
			seen[key] = true
		}
	}
	records = append(records, hff.Object(map[string]hff.Value{"data": hff.Null(), "key": hff.String("asset")}))
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var script strings.Builder
			fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s,key json,data_meta jsonb);\n", format)
			sqlNull := false
			for i, record := range records {
				data, _ := record.Lookup("data")
				key, _ := record.Lookup("key")
				document := sqlFixtureString(taggedCollectionJSON(t, data))
				if data.Kind() == hff.NullKind && !sqlNull {
					document = "NULL"
					sqlNull = true
				}
				fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s::%s,%s::json,%s::jsonb);\n", i, document, format, sqlFixtureString(taggedCollectionJSON(t, key)), sqlFixtureString(pairTypeMetadata(t, data)))
			}
			var expected []bool
			var labels []string
			for i, source := range structValuesSources() {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(source, err)
				}
				q, err := hffsql.Build(ctx, p, dynamicStructOptions(format, true)...)
				if err != nil {
					t.Fatal(source, err)
				}
				appendAggregateProfileQuery(t, &script, q, "records ORDER BY id", i)
				for j, record := range records {
					want, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(source, j, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s row%d", source, j))
				}
			}
			t.Logf("%d query-record results", len(expected))
			runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
		})
	}
}

func TestPostgreSQLStructValuesKeysAndMissing(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	// Required nullable fields may be absent. Enumeration includes actual keys,
	// including explicit nulls, in code-point order rather than declaration order.
	typ := schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(
		schema.Optional(schema.Timestamp("中")), schema.Nullable(schema.String("a")), schema.Optional(schema.Duration("é")), schema.Nullable(schema.Any("z")), schema.Optional(schema.Bytes("")),
	))))
	e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	data := []hff.Value{hff.Object(nil), hff.Null(), hff.Object(map[string]hff.Value{"a": hff.Null()}), hff.Object(map[string]hff.Value{"z": hff.Null()}), hff.Object(map[string]hff.Value{"": hff.Bytes([]byte("a"))}), hff.Object(map[string]hff.Value{"a": hff.String("a"), "z": hff.String("YQ=="), "é": hff.Duration(time.Nanosecond), "中": hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)), "": hff.Bytes([]byte("a"))}), hff.Object(map[string]hff.Value{"z": hff.Float(1)})}
	sources := []string{`data:values:size=0`, `data:values:count=1`, `data:values:count(v=>v:type="null")=1`, `data:values:map(v=>v:type):json=["bytes","string","string","duration","timestamp"]:json`, `data:values:map(v=>v:type):json:exists`, `data:values:any(v=>v:type="bytes")`, `data:values:all(v=>v:exists)`, `data:values:filter(v=>v:type="null"):size=1`, `data:values[0]:type="bytes"`, `data:values[-1]:type="timestamp"`, `data:values:map(v=>v:[bytes]):exists`, `data:values:filter(v=>false):all(v=>true)`}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		var script strings.Builder
		var expected []bool
		var labels []string
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(source, err)
			}
			var settings []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("records", "data_meta")))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, settings...)))
			if err != nil {
				t.Fatal(source, err)
			}
			for i, value := range data {
				appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(taggedCollectionJSON(t, value))+"::"+string(format)+","+sqlFixtureString(pairTypeMetadata(t, value))+"::jsonb)) records(data,data_meta)", len(expected))
				want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": value}))
				if err != nil {
					t.Fatal(source, i, err)
				}
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s row%d", source, i))
			}
		}
		t.Logf("%s %d key-order/missing results", format, len(expected))
		runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
		cancel()
	}
}

func TestPostgreSQLStructValuesPrepare(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	record := dynamicStructRecords()[0]
	data, _ := record.Lookup("data")
	key, _ := record.Lookup("key")
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range structValuesSources() {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filter.Match(ctx, record)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, dynamicStructOptions(format, true)...)
			if err != nil {
				t.Fatal(source, err)
			}
			var args []string
			for _, arg := range q.Args() {
				_, literal := sqlFixtureParameter(t, arg)
				args = append(args, literal)
			}
			name := fmt.Sprintf("struct_values_%d", len(expected))
			call := name
			if len(args) > 0 {
				call += "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM (VALUES (%s::%s,%s::json,%s::jsonb)) records(data,key,data_meta);EXECUTE %s;DEALLOCATE %s;\n", name, q.Where(), sqlFixtureString(taggedCollectionJSON(t, data)), format, sqlFixtureString(taggedCollectionJSON(t, key)), sqlFixtureString(pairTypeMetadata(t, data)), call, name)
			expected = append(expected, want)
			labels = append(labels, string(format)+" "+source)
		}
	}
	t.Logf("%d statements without parameter OIDs", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLStructValuesFloatBoundaries(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, nullable := range []bool{false, true} {
		rate := schema.Float("rate")
		if nullable {
			rate = schema.Nullable(rate)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(rate, schema.String("text"), schema.Bytes("asset"))))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var settings []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("records", "data_meta")))
			}
			options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, settings...))}
			for _, token := range []string{"1e-400", "-1e-400", "1e400", "5e-324", "2.2250738585072014e-308", "-0.0", "0.0", "1.0"} {
				invalid := token == "1e-400" || token == "-1e-400" || token == "1e400"
				raw := `{"rate":` + token + `,"asset":"YQ==","text":"YQ=="}`
				metadata := `{"version":1,"types":{"/rate":{"type":"float"}}}`
				if token == "-0.0" {
					metadata = `{"version":1,"types":{"/rate":{"type":"float","text":"-0.0"}}}`
				}
				for _, tc := range []struct {
					source string
					want   bool
				}{
					{`data:values[1]:type="null"`, invalid},
					{`data:values[1]:exists`, !invalid},
					{`data:values[1]:[null]=null`, invalid},
					{`data:values[1]:[float]:exists`, !invalid},
					{`data:values:map(v=>v:type):json=["bytes","null","string"]:json`, invalid},
					{`data:values:size=3`, true},
					{`data:values:count(v=>v:exists)=3`, !invalid},
					{`data:values:find(v=>v:type="number"):type="null"`, invalid},
				} {
					p, err := e.Compile(ctx, tc.source)
					if err != nil {
						t.Fatal(tc.source, err)
					}
					q, err := hffsql.Build(ctx, p, options...)
					if err != nil {
						t.Fatal(tc.source, err)
					}
					appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(raw)+"::"+string(format)+","+sqlFixtureString(metadata)+"::jsonb)) records(data,data_meta)", len(expected))
					expected = append(expected, tc.want)
					labels = append(labels, fmt.Sprintf("nullable=%t %s %s %s", nullable, format, token, tc.source))
				}
			}
		}
	}
	t.Logf("%d Float boundary results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLStructValuesNativeConstructor(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Bytes("asset")), schema.Nullable(schema.String("text")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const object = `{"text":text,"asset":asset}:values`
	sources := []string{object + `:any(v=>v:type="bytes")`, object + `:map(v=>v:type):json=["bytes","string"]:json`, object + `:size=2`, object + `:count(v=>v:exists)=2`, object + `[0]:type="bytes"`, `{"text":text,"asset":asset!}:values:filter(v=>false):all(v=>true)`, object + `:filter(v=>v:type="bytes"):map(v=>v:[bytes]:base64):json:exists`}
	records := []hff.Value{hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte("a")), "text": hff.String("YQ==")}), hff.Object(map[string]hff.Value{"asset": hff.Bytes(nil), "text": hff.String("")}), hff.Object(map[string]hff.Value{"asset": hff.Null(), "text": hff.String("YQ==")}), hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte{0, 255}), "text": hff.Null()}), hff.Object(map[string]hff.Value{"asset": hff.Null(), "text": hff.Null()})}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"asset"}, hffsql.Column("records", "asset")), hffsql.Bind(hff.Path{"text"}, hffsql.Column("records", "text"))))
		if err != nil {
			t.Fatal(source, err)
		}
		for i, record := range records {
			asset, _ := record.Lookup("asset")
			text, _ := record.Lookup("text")
			appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureValue(t, asset, "bytea")+","+sqlFixtureValue(t, text, "text")+")) records(asset,text)", len(expected))
			want, err := filter.Match(ctx, record)
			if err != nil {
				t.Fatal(source, i, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s row%d", source, i))
		}
	}
	t.Logf("%d native constructor results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
