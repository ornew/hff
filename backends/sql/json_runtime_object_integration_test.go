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

func TestPostgreSQLRuntimeObjectEnumerationMatchLocal(t *testing.T) {
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
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			var script strings.Builder
			script.WriteString("SET application_name='hff-runtime-object-enumeration';SET jit=off;SET statement_timeout='45s';\n")
			fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s,key json,data_meta jsonb);\n", format)
			for i, record := range records {
				data, _ := record.Lookup("data")
				key, _ := record.Lookup("key")
				document := sqlFixtureString(taggedCollectionJSON(t, data))
				if data.Kind() == hff.NullKind {
					document = "NULL"
				}
				fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s::%s,%s::json,%s::jsonb);\n", i, document, format, sqlFixtureString(taggedCollectionJSON(t, key)), sqlFixtureString(pairTypeMetadata(t, data)))
			}
			var expected []bool
			var labels []string
			for i, source := range runtimeObjectSources() {
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
				var args []string
				for _, arg := range q.Args() {
					_, literal := sqlFixtureParameter(t, arg)
					args = append(args, literal)
				}
				name := fmt.Sprintf("runtime_object_%d", i)
				call := name
				if len(args) > 0 {
					call += "(" + strings.Join(args, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM records WHERE id=0;EXECUTE %s;DEALLOCATE %s;\n", name, q.Where(), call, name)
				want, err := filter.Match(ctx, records[0])
				if err != nil {
					t.Fatal(err)
				}
				expected = append(expected, want)
				labels = append(labels, "inferred OID "+source)
			}
			t.Logf("%d query-record results", len(expected))
			runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
		})
	}
}

func TestPostgreSQLRuntimeObjectKeyProvenance(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(schema.Bytes("asset"), schema.Float("rate"), schema.String("text"))), schema.Any("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	script.WriteString("SET application_name='hff-runtime-object-keys';SET jit=off;SET statement_timeout='20s';\n")
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, number := range []float64{1, math.Copysign(0, -1)} {
			data := hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte("a")), "rate": hff.Float(number), "text": hff.String("text")})
			record := hff.Object(map[string]hff.Value{"data": data, "key": hff.Null()})
			for _, source := range runtimeObjectKeySources() {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, dynamicStructOptions(format, false)...)
				if err != nil {
					t.Fatal(source, err)
				}
				from := fmt.Sprintf("(VALUES(%s::%s,'null'::json))records(data,key)", sqlFixtureString(taggedCollectionJSON(t, data)), format)
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				want, err := filter.Match(ctx, record)
				if err != nil {
					t.Fatal(err)
				}
				expected = append(expected, want)
				labels = append(labels, string(format)+" "+source)
			}
		}
	}
	t.Logf("%d key results without type metadata", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLRuntimeObjectCookbook(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("labels", schema.Union(schema.Struct(schema.String("app"), schema.Bytes("asset")), schema.IntType, schema.ListOf(schema.BytesType)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	script.WriteString(`SET jit=off;SET statement_timeout='20s';CREATE TEMP TABLE typed_events(id integer PRIMARY KEY,labels jsonb);
INSERT INTO typed_events VALUES(1,'{"app":"auth","asset":"YQ=="}'),(2,'{"app":"billing","asset":"Yg=="}'),(3,'0'),(4,'["YQ=="]');`)
	var expected []bool
	var labels []string
	for i, source := range []string{
		`labels:items:filter(pair=>pair[0]="asset"):foldl({"v":null},(acc,pair)=>{"v":pair[1]}):values:map(value=>value:type):json=["bytes"]:json`,
		`labels:items:filter(pair=>pair[0]="asset"):foldr({"v":null},(pair,acc)=>{"v":pair[1]}):items:map(pair=>pair[1]:type):json=["bytes"]:json`,
		`labels:items:filter(pair=>pair[0]="asset"):foldl({},(acc,pair)=>{"v":pair[1]}):keys:json=["v"]:json`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("typed_events", "labels"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
		appendAggregateProfileQuery(t, &script, q, "typed_events ORDER BY id", i)
		for id := 1; id <= 4; id++ {
			expected = append(expected, id <= 2)
			labels = append(labels, fmt.Sprintf("%s id%d", source, id))
		}
	}
	t.Logf("%d exact cookbook results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
