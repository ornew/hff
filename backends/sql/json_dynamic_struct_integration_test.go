//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
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

func dynamicStructRecords() []hff.Value {
	var documents []hff.Value
	for _, dynamic := range []hff.Value{hff.String("YQ=="), hff.String("2026-01-01T00:00:00Z"), hff.Int(9007199254740993), hff.Float(1), hff.Float(math.Copysign(0, -1)), hff.List(hff.Float(1), hff.String("YQ==")), hff.List(), hff.Object(map[string]hff.Value{"v": hff.Float(1)}), hff.Bool(false), hff.Null()} {
		documents = append(documents, hff.Object(map[string]hff.Value{
			"text": hff.String("YQ=="), "asset": hff.Bytes([]byte("a")), "elapsed": hff.Duration(time.Nanosecond), "recorded": hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)), "rate": hff.Float(math.Copysign(0, -1)), "dynamic": dynamic, "alwaysNull": hff.Null(),
		}))
	}
	documents = append(documents, hff.Null(), hff.Object(nil), hff.Object(map[string]hff.Value{"asset": hff.Null(), "text": hff.Null(), "dynamic": hff.Null(), "rate": hff.Null()}), hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte{0, 255}), "text": hff.String("日本語"), "rate": hff.Float(1)}))
	var records []hff.Value
	for _, document := range documents {
		for _, key := range []hff.Value{hff.String("asset"), hff.String("text"), hff.String("elapsed"), hff.String("recorded"), hff.String("rate"), hff.String("dynamic"), hff.String("alwaysNull"), hff.String("missing"), hff.String(""), hff.Null(), hff.Int(1), hff.Float(1), hff.Bool(false), hff.List(), hff.Object(nil)} {
			records = append(records, hff.Object(map[string]hff.Value{"data": document, "key": key}))
		}
	}
	return records
}

func runDynamicStructParity(t *testing.T, stringKeys bool) {
	t.Helper()
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	typ := dynamicStructSchema()
	sources := dynamicStructSources()
	sources = append(sources, `data:get(key):json="-0.0"`, `data[key]:json="1.0"`, `data:get(key):[list[bytes]]:exists`, `data:get(key):[map[bytes]]:exists`, `["asset","text"]:any(key=>["elapsed"]:any(key=>data:get(key):type="duration") data:get(key):exists)`)
	if stringKeys {
		fields := typ.Fields()
		fields[1] = schema.Nullable(schema.String("key"))
		typ = schema.Struct(fields...)
		sources = []string{`data:get(key):type="bytes"`, `data[key]:type="string"`, `data:get(key):[bytes]:base64="YQ=="`, `data:get(key!):type="null"`, `data[key!]=null`, `data[key]:json="-0.0"`}
	}
	e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	records := dynamicStructRecords()
	if stringKeys {
		var selected []hff.Value
		for _, record := range records {
			key, _ := record.Lookup("key")
			if key.Kind() == hff.StringKind || key.Kind() == hff.NullKind {
				selected = append(selected, record)
			}
		}
		records = selected
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			var script strings.Builder
			fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s,key json,data_meta jsonb);\n", format)
			for i, record := range records {
				data, _ := record.Lookup("data")
				key, _ := record.Lookup("key")
				document := sqlFixtureString(taggedCollectionJSON(t, data))
				if data.Kind() == hff.NullKind && i%2 == 0 {
					document = "NULL"
				}
				fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s::%s,%s::json,%s::jsonb);\n", i, document, format, sqlFixtureString(taggedCollectionJSON(t, key)), sqlFixtureString(pairTypeMetadata(t, data)))
			}
			var expected []bool
			var labels []string
			for i, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
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

func TestPostgreSQLDynamicStructAccessMatchLocal(t *testing.T)     { runDynamicStructParity(t, false) }
func TestPostgreSQLDynamicStructStringKeysMatchLocal(t *testing.T) { runDynamicStructParity(t, true) }

func TestPostgreSQLDynamicStructPrepare(t *testing.T) {
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
		for _, source := range dynamicStructSources() {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
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
			name := fmt.Sprintf("dynamic_struct_%d", len(expected))
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

// A declared Float can decode to ordinary null under PostgreSQL's Float
// model. Dynamic selection must observe the decoded value, just like a
// direct field access; a JSON number category alone is insufficient.
func TestPostgreSQLDynamicStructFloatBoundaries(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, nullable := range []bool{false, true} {
		rate := schema.Float("rate")
		if nullable {
			rate = schema.Nullable(rate)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(rate, schema.String("text"), schema.Bytes("asset"))), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var settings []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
			}
			options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, settings...), hffsql.Bind(hff.Path{"key"}, hffsql.Column("records", "key")))}
			for _, token := range []string{"1e-400", "-1e-400", "1e400", "5e-324", "2.2250738585072014e-308", "-0.0", "0.0", "1.0"} {
				invalid := token == "1e-400" || token == "-1e-400" || token == "1e400"
				raw := `{"rate":` + token + `,"text":"YQ==","asset":"YQ=="}`
				metadata := `{"version":1,"types":{"/rate":{"type":"float"}}}`
				if token == "-0.0" {
					metadata = `{"version":1,"types":{"/rate":{"type":"float","text":"-0.0"}}}`
				}
				for _, access := range []string{"data.rate", "data:get(key)", "data[key]"} {
					for _, tc := range []struct {
						suffix string
						want   bool
					}{
						{`:type="null"`, invalid}, {`:exists`, !invalid},
						{`:[null]=null`, invalid}, {`:[float]:exists`, !invalid},
						{`:[float]!:exists`, !invalid}, {`:[float]:type="null"`, false},
						{`:json="null"`, invalid},
					} {
						source := access + tc.suffix
						p, err := e.Compile(ctx, source)
						if err != nil {
							t.Fatal(source, err)
						}
						q, err := hffsql.Build(ctx, p, options...)
						if err != nil {
							t.Fatal(source, err)
						}
						appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(raw)+"::"+string(format)+",'rate'::text,"+sqlFixtureString(metadata)+"::jsonb)) records(data,key,metadata)", len(expected))
						expected = append(expected, tc.want)
						labels = append(labels, fmt.Sprintf("nullable=%t %s %s %s", nullable, format, token, source))
					}
				}
			}
		}
	}
	t.Logf("%d Float boundary results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
