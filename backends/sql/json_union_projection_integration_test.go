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

func unionProjectionRecords(name string, callbacks bool) []hff.Value {
	values := []hff.Value{
		hff.Object(map[string]hff.Value{"safe": hff.String("ok"), "n": hff.Int(1), "nums": hff.List(hff.Int(1), hff.Int(2), hff.Int(3)), "bad": hff.String("YQ=="), "a/b~雪": hff.String("yes")}),
		hff.Object(map[string]hff.Value{"safe": hff.String("other"), "n": hff.Int(3), "nums": hff.List(), "bad": hff.Bytes([]byte("a"))}),
		hff.Object(map[string]hff.Value{"safe": hff.Null(), "n": hff.Null(), "nums": hff.Null(), "bad": hff.Bytes([]byte("a"))}),
		hff.Object(map[string]hff.Value{"bad": hff.String("a")}),
		hff.Object(map[string]hff.Value{"safe": hff.String(""), "n": hff.Int(0), "nums": hff.List(hff.Int(0)), "bad": hff.Bytes(nil)}),
		hff.Object(map[string]hff.Value{"safe": hff.String("ok"), "n": hff.Int(-2), "nums": hff.List(hff.Int(-2), hff.Int(1)), "bad": hff.Bytes([]byte{0, 255}), "a/b~雪": hff.String("no")}),
	}
	switch name {
	case "struct-or-int":
		values = append(values, hff.Int(0), hff.Int(8))
	case "struct-or-list":
		values = append(values, hff.List(), hff.List(hff.Bytes(nil)), hff.List(hff.Bytes([]byte("a"))))
	}
	values = append(values, hff.Null())
	if callbacks {
		reversed := make([]hff.Value, len(values))
		for i, value := range values {
			reversed[len(values)-i-1] = value
		}
		values = []hff.Value{hff.List(values...), hff.List(reversed...), hff.List(), hff.Null(), hff.List(hff.Null()), hff.List(values[0], values[1]), hff.List(values[1], values[0])}
	}
	var records []hff.Value
	for _, value := range values {
		records = append(records, hff.Object(map[string]hff.Value{"data": value}))
	}
	return records
}

func TestPostgreSQLUnionProjectionsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	for _, tc := range unionProjectionTypes() {
		for _, callbacks := range []bool{false, true} {
			typ, sources := tc.typ, unionProjectionSources()
			if callbacks {
				typ, sources = schema.ListOf(schema.Union(tc.typ, schema.NullType)), unionProjectionCallbackSources()
			}
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", typ)))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				for _, metadata := range []bool{false, true} {
					if format == hffsql.JSON && metadata {
						continue
					}
					t.Run(fmt.Sprintf("%s/callbacks=%t/%s/metadata=%t", tc.name, callbacks, format, metadata), func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
						defer cancel()
						records := unionProjectionRecords(tc.name, callbacks)
						var script strings.Builder
						fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s,metadata jsonb);\n", format)
						for i, record := range records {
							data, _ := record.Lookup("data")
							document := sqlFixtureString(taggedCollectionJSON(t, data))
							if data.Kind() == hff.NullKind {
								// Exercise both representations of an HFF null root.
								fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,NULL,%s::jsonb);\n", len(records), sqlFixtureString(pairTypeMetadata(t, data)))
							}
							fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s::%s,%s::jsonb);\n", i, document, format, sqlFixtureString(pairTypeMetadata(t, data)))
						}
						records = append(records, hff.Object(map[string]hff.Value{"data": hff.Null()}))
						var expected []bool
						var labels []string
						for i, source := range sources {
							p, err := e.Compile(ctx, source)
							if err != nil {
								t.Fatal(source, err)
							}
							filter, err := local.Build(ctx, p)
							if err != nil {
								t.Fatal(source, err)
							}
							q, err := hffsql.Build(ctx, p, unionProjectionOptions(format, metadata)...)
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
		}
	}
}

func TestPostgreSQLUnionProjectionsPrepare(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, tc := range unionProjectionTypes() {
		for _, callbacks := range []bool{false, true} {
			typ, sources := tc.typ, unionProjectionSources()
			if callbacks {
				typ, sources = schema.ListOf(schema.Union(tc.typ, schema.NullType)), unionProjectionCallbackSources()
			}
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", typ)))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			record := unionProjectionRecords(tc.name, callbacks)[0]
			data, _ := record.Lookup("data")
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				for _, metadata := range []bool{false, true} {
					if format == hffsql.JSON && metadata {
						continue
					}
					for _, source := range sources {
						p, err := e.Compile(ctx, source)
						if err != nil {
							t.Fatal(source, err)
						}
						filter, err := local.Build(ctx, p)
						if err != nil {
							t.Fatal(source, err)
						}
						want, err := filter.Match(ctx, record)
						if err != nil {
							t.Fatal(source, err)
						}
						q, err := hffsql.Build(ctx, p, unionProjectionOptions(format, metadata)...)
						if err != nil {
							t.Fatal(source, err)
						}
						var args []string
						for _, arg := range q.Args() {
							_, literal := sqlFixtureParameter(t, arg)
							args = append(args, literal)
						}
						name := fmt.Sprintf("union_projection_%d", len(expected))
						call := name
						if len(args) > 0 {
							call += "(" + strings.Join(args, ",") + ")"
						}
						fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb)) records(data,metadata);EXECUTE %s;DEALLOCATE %s;\n", name, q.Where(), sqlFixtureString(taggedCollectionJSON(t, data)), format, sqlFixtureString(pairTypeMetadata(t, data)), call, name)
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s callbacks=%t %s metadata=%t %s", tc.name, callbacks, format, metadata, source))
					}
				}
			}
		}
	}
	t.Logf("%d statements without parameter OIDs", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLUnionProjectionsFloatBoundaries(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, callbacks := range []bool{false, true} {
		object := schema.Struct(schema.Nullable(schema.Float("n")), schema.Field("bad", schema.Union(schema.StringType, schema.BytesType)))
		typ := schema.Union(object, schema.IntType)
		if callbacks {
			typ = schema.ListOf(typ)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for _, token := range []string{"1e-400", "-1e-400", "1e400", "5e-324", "2.2250738585072014e-308", "-0.0", "0.0", "1.0"} {
				invalid := token == "1e-400" || token == "-1e-400" || token == "1e400"
				raw := `{"n":` + token + `,"bad":"YQ=="}`
				pointer := "/n"
				if callbacks {
					raw, pointer = "["+raw+"]", "/0/n"
				}
				entry := `{"type":"float"}`
				if token == "-0.0" {
					entry = `{"type":"float","text":"-0.0"}`
				}
				metadata := `{"version":1,"types":{"` + pointer + `":` + entry + `}}`
				cases := []struct {
					source string
					want   bool
				}{
					{`data.n:type="null"`, invalid}, {`data.n:exists`, !invalid},
					{`data.n=null`, invalid}, {`data.n:[float]:exists`, !invalid},
					{`data!.n:exists`, !invalid}, {`data.n:json="null"`, invalid},
				}
				if callbacks {
					cases = []struct {
						source string
						want   bool
					}{
						{`data:any(x=>x.n:type="null")`, invalid}, {`data:any(x=>x.n:exists)`, !invalid},
						{`data:map(x=>x.n):any(v=>v:type="null")`, invalid},
						{`data:any(x=>x.n:[float]:exists)`, !invalid},
						{`data:map(x=>x.n!):filter(v=>false):count=0`, !invalid},
						{`data:any(x=>x.n:json="null")`, invalid},
					}
				}
				for _, tc := range cases {
					p, err := e.Compile(ctx, tc.source)
					if err != nil {
						t.Fatal(tc.source, err)
					}
					q, err := hffsql.Build(ctx, p, unionProjectionOptions(format, format == hffsql.JSONB)...)
					if err != nil {
						t.Fatal(tc.source, err)
					}
					from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb)) records(data,metadata)", sqlFixtureString(raw), format, sqlFixtureString(metadata))
					appendAggregateProfileQuery(t, &script, q, from, len(expected))
					expected = append(expected, tc.want)
					labels = append(labels, fmt.Sprintf("callbacks=%t %s %s %s", callbacks, format, token, tc.source))
				}
			}
		}
	}
	t.Logf("%d backend Float boundary results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLUnionProjectionCookbook(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	objectType := schema.Struct(schema.String("app"), schema.Field("opaque", schema.Union(schema.StringType, schema.BytesType)))
	labelsType := schema.Union(objectType, schema.IntType, schema.ListOf(schema.BytesType))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", labelsType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	script.WriteString(`CREATE TEMP TABLE projection_events(id bigint PRIMARY KEY,labels jsonb NOT NULL);
INSERT INTO projection_events VALUES
(1,'{"app":"auth","opaque":"YQ=="}'),
(2,'{"app":"billing","opaque":"Yg=="}'),
(3,'0'),
(4,'["YQ=="]');`)
	records := []hff.Value{
		hff.Object(map[string]hff.Value{"app": hff.String("auth"), "opaque": hff.String("YQ==")}),
		hff.Object(map[string]hff.Value{"app": hff.String("billing"), "opaque": hff.Bytes([]byte("b"))}),
		hff.Int(0), hff.List(hff.Bytes([]byte("a"))),
	}
	var expected []bool
	var labels []string
	for i, source := range []string{`labels.app="auth"`, `labels:get("app")="auth"`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("projection_events", "labels"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
		appendAggregateProfileQuery(t, &script, query, "projection_events ORDER BY id", i)
		for j, record := range records {
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"labels": record}))
			if err != nil || want != (j == 0) {
				t.Fatal(source, j+1, want, err)
			}
			expected = append(expected, j == 0)
			labels = append(labels, fmt.Sprintf("%s row%d", source, j+1))
		}
	}
	t.Logf("%d cookbook results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
