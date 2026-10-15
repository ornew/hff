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

func structRootUnionRecords() []hff.Value {
	records := dynamicStructRecords()
	keys := []hff.Value{hff.String("asset"), hff.String("text"), hff.String("missing"), hff.Int(0), hff.Int(1), hff.Int(-1), hff.Int(9), hff.Int(1 << 32), hff.Float(0), hff.Float(-1), hff.Float(1.5), hff.Null(), hff.Bool(false)}
	for _, data := range []hff.Value{hff.Int(0), hff.Int(10), hff.List(), hff.List(hff.Bytes([]byte("a")), hff.Bytes(nil), hff.Bytes([]byte("x")))} {
		for _, key := range keys {
			records = append(records, hff.Object(map[string]hff.Value{"data": data, "key": key}))
		}
	}
	return records
}

func TestPostgreSQLStructRootUnionMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	for _, keyType := range []hff.Type{schema.AnyType, schema.StringType} {
		e, err := engine.New(engine.WithSchema(structRootUnionSchema(keyType)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		var records []hff.Value
		for _, record := range structRootUnionRecords() {
			key, _ := record.Lookup("key")
			if keyType.Kind() == hff.StringType && key.Kind() != hff.StringKind && key.Kind() != hff.NullKind {
				continue
			}
			records = append(records, record)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(fmt.Sprintf("%v/%s", keyType.Kind(), format), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
				for i, source := range structRootUnionSources() {
					if keyType.Kind() == hff.StringType && source == `data[key:[number]]:type="bytes"` {
						continue
					}
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
					var args []string
					for _, arg := range q.Args() {
						_, literal := sqlFixtureParameter(t, arg)
						args = append(args, literal)
					}
					name := fmt.Sprintf("root_union_%d", i)
					call := name
					if len(args) > 0 {
						call += "(" + strings.Join(args, ",") + ")"
					}
					fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM records WHERE id IN(0,%d) ORDER BY id;EXECUTE %s;DEALLOCATE %s;\n", name, q.Where(), len(records)-1, call, name)
					for _, j := range []int{0, len(records) - 1} {
						want, err := filter.Match(ctx, records[j])
						if err != nil {
							t.Fatal(source, err)
						}
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("PREPARE %s row%d", source, j))
					}
				}
				t.Logf("%d query-record results", len(expected))
				runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
			})
		}
	}
}

func TestPostgreSQLStructRootUnionNumericBranches(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	object := schema.Struct(schema.Bytes("asset"), schema.String("text"))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(object, schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType))), schema.Any("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, value := range []hff.Value{hff.Int(9007199254740993), hff.Float(1), hff.Float(math.Copysign(0, -1))} {
			data := hff.List(value)
			record := hff.Object(map[string]hff.Value{"data": data, "key": hff.Int(-1)})
			for _, source := range []string{`data[key]:type="number"`, `data[key]:json="1.0"`, `data[key]:json="-0.0"`, `data[key]:[int]=9007199254740993`, `data[key]:[float]:exists`, `data[key:[number]]:exists`} {
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
				from := fmt.Sprintf("(VALUES (%s::%s,'-1'::json,%s::jsonb)) records(data,key,data_meta)", sqlFixtureString(taggedCollectionJSON(t, data)), format, sqlFixtureString(pairTypeMetadata(t, data)))
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				want, err := filter.Match(ctx, record)
				if err != nil {
					t.Fatal(err)
				}
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s %v %s", format, value.Kind(), source))
			}
		}
	}
	t.Logf("%d numeric union results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLStructRootUnionFloatBoundaries(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	object := schema.Struct(schema.Bytes("asset"), schema.String("text"))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(object, schema.ListOf(schema.FloatType))), schema.Any("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, token := range []string{"1e-400", "1e400", "1.0"} {
			invalid := token != "1.0"
			for _, tc := range []struct {
				source string
				want   bool
			}{{`data[key]:type="null"`, invalid}, {`data[key]:exists`, !invalid}, {`data[key]:[float]:exists`, !invalid}, {`data[key]:[null]:exists`, false}} {
				p, err := e.Compile(ctx, tc.source)
				if err != nil {
					t.Fatal(tc.source, err)
				}
				q, err := hffsql.Build(ctx, p, dynamicStructOptions(format, true)...)
				if err != nil {
					t.Fatal(tc.source, err)
				}
				from := fmt.Sprintf("(VALUES ('[%s]'::%s,'0'::json,'{\"version\":1,\"types\":{\"/0\":{\"type\":\"float\"}}}'::jsonb)) records(data,key,data_meta)", token, format)
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, tc.want)
				labels = append(labels, fmt.Sprintf("%s %s %s", format, token, tc.source))
			}
		}
	}
	t.Logf("%d Float boundary results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLStructRootUnionCookbook(t *testing.T) {
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
	script.WriteString(`CREATE TEMP TABLE typed_events(id integer PRIMARY KEY, labels jsonb);
INSERT INTO typed_events VALUES (1,'{"app":"auth","asset":"YQ=="}'),(2,'{"app":"billing","asset":"Yg=="}'),(3,'0'),(4,'["YQ=="]');`)
	var expected []bool
	var labels []string
	for i, tc := range []struct {
		source  string
		matches []int
	}{
		{`labels:values:map(value=>value:type):json=["string","bytes"]:json`, []int{1, 2}},
		{`["app","asset"]:map(key=>labels:get(key)):map(value=>value:type):json=["string","bytes"]:json`, []int{1, 2}},
		{`[0]:any(key=>labels[key]:[bytes]:base64="YQ==")`, []int{4}},
	} {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("typed_events", "labels"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		appendAggregateProfileQuery(t, &script, q, "typed_events ORDER BY id", i)
		for id := 1; id <= 4; id++ {
			want := false
			for _, match := range tc.matches {
				want = want || id == match
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s id%d", tc.source, id))
		}
	}
	t.Logf("%d exact cookbook results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLStructRootUnionStringIndex(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, element := range []hff.Type{schema.Union(schema.StringType, schema.BytesType), schema.FloatType} {
		object := schema.Struct(schema.Bytes("asset"), schema.String("text"))
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", schema.Union(object, schema.ListOf(element)))), schema.Nullable(schema.String("key")))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		list := hff.List(hff.String("YQ=="), hff.Bytes([]byte("a")))
		if element.Kind() == hff.FloatType {
			list = hff.List(hff.Float(math.Copysign(0, -1)))
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for _, data := range []hff.Value{hff.Object(map[string]hff.Value{"asset": hff.Bytes([]byte("a")), "text": hff.String("YQ==")}), list, hff.Null()} {
				for _, key := range []hff.Value{hff.String("asset"), hff.Null()} {
					for _, source := range []string{`data[key]:json:exists`, `data[key!]:type="null"`, `data:get(key!):type="null"`} {
						p, err := e.Compile(ctx, source)
						if err != nil {
							t.Fatal(source, err)
						}
						filter, err := local.Build(ctx, p)
						if err != nil {
							t.Fatal(err)
						}
						q, err := hffsql.Build(ctx, p, dynamicStructOptions(format, false)...)
						if err != nil {
							t.Fatal(source, err)
						}
						from := fmt.Sprintf("(VALUES (%s::%s,%s::json)) records(data,key)", sqlFixtureString(taggedCollectionJSON(t, data)), format, sqlFixtureString(taggedCollectionJSON(t, key)))
						appendAggregateProfileQuery(t, &script, q, from, len(expected))
						want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": data, "key": key}))
						if err != nil {
							t.Fatal(err)
						}
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s element%v %s", format, element.Kind(), source))
					}
				}
			}
		}
	}
	t.Logf("%d unobserved array results without metadata", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
