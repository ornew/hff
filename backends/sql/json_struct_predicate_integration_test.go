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

func structPredicateRecords() []hff.Value {
	var records []hff.Value
	for i, record := range dynamicStructRecords() {
		data, _ := record.Lookup("data")
		key, _ := record.Lookup("key")
		text, _ := key.AsString()
		if i < 15 || data.Kind() == hff.NullKind || text == "asset" || text == "text" || text == "dynamic" || text == "missing" {
			records = append(records, record)
		}
	}
	return records
}

func TestPostgreSQLStructPredicatesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	records := structPredicateRecords()
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var script strings.Builder
			fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s,key json,data_meta jsonb);\n", format)
			nullAsSQL := false
			for i, record := range records {
				data, _ := record.Lookup("data")
				key, _ := record.Lookup("key")
				document := sqlFixtureString(taggedCollectionJSON(t, data))
				if data.Kind() == hff.NullKind && !nullAsSQL {
					document = "NULL"
					nullAsSQL = true
				}
				fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s::%s,%s::json,%s::jsonb);\n", i, document, format, sqlFixtureString(taggedCollectionJSON(t, key)), sqlFixtureString(pairTypeMetadata(t, data)))
			}
			var expected []bool
			var labels []string
			for i, source := range structPredicateSources() {
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

func TestPostgreSQLStructPredicatesPrepare(t *testing.T) {
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
		for _, source := range structPredicateSources() {
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
			name := fmt.Sprintf("struct_predicate_%d", len(expected))
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

func TestPostgreSQLStructPredicatesFloatBoundaries(t *testing.T) {
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
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(rate, schema.String("text"), schema.Bytes("asset"))), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var settings []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("records", "data_meta")))
			}
			options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, settings...), hffsql.Bind(hff.Path{"key"}, hffsql.Column("records", "key")))}
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
					{`["rate"]:map(k=>data[k]):count(v=>v:type="null")=1`, invalid},
					{`["rate","asset"]:map(k=>data[k]):count(v=>v:[float]:exists)=1`, !invalid},
					{`[data:get(key)]:flatmap(v=>[v]):any(v=>v:type="null")`, invalid},
					{`[data:get(key)]:flatmap(v=>[v]):all(v=>v:exists)`, !invalid},
					{`[data:get(key)]:flatmap(v=>[v]):map(v=>v!):filter(v=>false):count(v=>true)=0`, !invalid},
					{`["rate"]:map(k=>data[k]!):filter(v=>false):count(v=>true)=0`, !invalid},
				} {
					p, err := e.Compile(ctx, tc.source)
					if err != nil {
						t.Fatal(tc.source, err)
					}
					q, err := hffsql.Build(ctx, p, options...)
					if err != nil {
						t.Fatal(tc.source, err)
					}
					appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(raw)+"::"+string(format)+",'rate'::text,"+sqlFixtureString(metadata)+"::jsonb)) records(data,key,data_meta)", len(expected))
					expected = append(expected, tc.want)
					labels = append(labels, fmt.Sprintf("nullable=%t %s %s %s", nullable, format, token, tc.source))
				}
			}
		}
	}
	t.Logf("%d Float boundary results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLStructPredicatesNativeConstructor(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Bytes("asset")), schema.Nullable(schema.String("text")), schema.Nullable(schema.String("key")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const object = `{"asset":asset,"text":text}`
	sources := []string{
		`["asset","text"]:map(k=>` + object + `[k]):count(v=>v:len>0)=2`,
		`[` + object + `:get(key)]:flatmap(v=>[v]):any(v=>v:type="bytes")`,
		`[` + object + `[key]]:flatmap(v=>[v]):all(v=>v:exists)`,
		`[` + object + `:get(key!)]:flatmap(v=>[v]):count(v=>v:type="null")=1`,
		`[` + object + `:get(key)]:flatmap(v=>[v]):map(v=>v!):filter(v=>false):count(v=>true)=0`,
		`[{"asset":asset!,"text":text}:get(key)]:flatmap(v=>[v]):any(v=>v:type="string")`,
	}
	payloads := []hff.Value{hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte("a")), "text": hff.String("YQ==")}), hff.Object(map[string]hff.Value{"asset": hff.Bytes(nil), "text": hff.String("")}), hff.Object(map[string]hff.Value{"asset": hff.Null(), "text": hff.String("YQ==")}), hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte{0, 255}), "text": hff.Null()}), hff.Object(map[string]hff.Value{"asset": hff.Null(), "text": hff.Null()})}
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
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"asset"}, hffsql.Column("records", "asset")), hffsql.Bind(hff.Path{"text"}, hffsql.Column("records", "text")), hffsql.Bind(hff.Path{"key"}, hffsql.Column("records", "key"))))
		if err != nil {
			t.Fatal(source, err)
		}
		for i, payload := range payloads {
			for _, key := range []hff.Value{hff.String("asset"), hff.String("text"), hff.String("missing"), hff.Null()} {
				asset, _ := payload.Lookup("asset")
				text, _ := payload.Lookup("text")
				record := hff.Object(map[string]hff.Value{"asset": asset, "text": text, "key": key})
				appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureValue(t, asset, "bytea")+","+sqlFixtureValue(t, text, "text")+","+sqlFixtureValue(t, key, "text")+")) records(asset,text,key)", len(expected))
				want, err := filter.Match(ctx, record)
				if err != nil {
					t.Fatal(source, i, err)
				}
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s row%d key%s", source, i, taggedCollectionJSON(t, key)))
			}
		}
	}
	t.Logf("%d native constructor results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
