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

func TestPostgreSQLJSONItemConcatPipelinesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemConcatPipelineSources, itemMapPipelineFixtures()...)
}

func TestPostgreSQLConcatPipelineDifferentFamiliesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	families := concatDifferentFamilies()
	for i, left := range families {
		for _, right := range families[i+1:] {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", left.typ), schema.Field("right", right.typ), schema.Int("index"), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range concatDifferentFamilySources() {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(source, err)
				}
				for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
					q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "lhs"), format), hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "rhs"), format), hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "idx")), hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
					if err != nil {
						t.Fatal(source, err)
					}
					obj := func(value hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"app": value}) }
					for _, index := range []int64{-2, -1, 0, 1, 2} {
						for _, pair := range [][2]hff.Value{{obj(left.first), obj(right.first)}, {obj(hff.Null()), obj(hff.Null())}, {hff.Object(nil), hff.Object(nil)}, {hff.Null(), obj(right.first)}, {obj(left.first), hff.Null()}} {
							serialized := concatProjectedJSON(t, pair, index)
							record := hff.Object(map[string]hff.Value{"left": pair[0], "right": pair[1], "index": hff.Int(index), "expected": hff.String(serialized)})
							want, err := filter.Match(ctx, record)
							if err != nil {
								t.Fatal(err)
							}
							from := fmt.Sprintf("(VALUES (%s::%s,%s::%s,%d::bigint,%s::text)) records(lhs,rhs,idx,expected)", sqlFixtureString(taggedCollectionJSON(t, pair[0])), format, sqlFixtureString(taggedCollectionJSON(t, pair[1])), format, index, sqlFixtureString(serialized))
							appendAggregateProfileQuery(t, &script, q, from, len(expected))
							expected = append(expected, want)
							labels = append(labels, fmt.Sprintf("%s/%s index%d %s", left.tag, right.tag, index, source))
						}
					}
				}
			}
		}
	}
	t.Logf("%d query-record results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

// Construct expected [type, JSON text] rows from explicit fixture values;
// neither backend's serializer defines this oracle.
func concatProjectedJSON(t *testing.T, pair [2]hff.Value, index int64) string {
	t.Helper()
	var rows []hff.Value
	for _, object := range pair {
		fields, ok := object.AsObject()
		if !ok {
			return "null"
		}
		value, exists := fields["app"]
		if !exists {
			continue
		}
		if index == 0 || index == -2 {
			value = hff.String("app")
		} else if index != 1 && index != -1 {
			value = hff.Null()
		}
		name := "null"
		switch value.Kind() {
		case hff.StringKind:
			name = "string"
		case hff.BytesKind:
			name = "bytes"
		case hff.DurationKind:
			name = "duration"
		case hff.TimestampKind:
			name = "timestamp"
		}
		rows = append(rows, hff.List(hff.String(name), hff.String(taggedCollectionJSON(t, value))))
	}
	return taggedCollectionJSON(t, hff.List(rows...))
}

func TestPostgreSQLConcatPipelineCookbook(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("labels", schema.Struct(
		schema.Nullable(schema.Field("app", schema.AnyType)), schema.Nullable(schema.Bytes("asset")),
		schema.Nullable(schema.Duration("elapsed")), schema.Nullable(schema.Timestamp("recorded")),
	))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Builder{}
	script.WriteString(`CREATE TEMP TABLE events(id integer,labels jsonb,labels_meta jsonb DEFAULT '{"version":1,"types":{}}'::jsonb);
INSERT INTO events(id,labels) VALUES
(1,'{"app":"YQ==","asset":"YQ==","elapsed":"0.000000001s","recorded":"2026-10-15T00:00:00Z"}'),
(2,'{"app":"2026-10-15T00:00:00Z","asset":"Yg==","elapsed":"1s","recorded":"2026-10-15T00:00:01Z"}'),
(3,'{"app":"","asset":null,"elapsed":"2s","recorded":"2026-10-15T00:00:02Z"}'),
(4,'{"app":null,"asset":null,"elapsed":null,"recorded":null}');`)
	const joined = `labels:items:map(pair=>pair[1]):concat(labels:items:map(pair=>pair[1]))`
	const types = `["string","bytes","duration","timestamp","string","bytes","duration","timestamp"]`
	var expected []bool
	var labels []string
	for i, source := range []string{
		joined + `:map(value=>value:type)=` + types,
		`labels:items:map(pair=>pair[1]):flatmap(value=>[value,value]):map(value=>value:type)=["string","string","bytes","bytes","duration","duration","timestamp","timestamp"]`,
		joined + `:map(value=>value:[any]):concat(labels:items:map(pair=>pair[1])):map(value=>value:type)=["string","bytes","duration","timestamp","string","bytes","duration","timestamp","string","bytes","duration","timestamp"]`,
		joined + `:filter(value=>value:type!="null"):map(value=>value:[any]):map(value=>value:type)=` + types,
		joined + `:map(value=>value!):filter(value=>false):all(value=>true)`,
		`labels:items:map(pair=>pair[1]):map(value=>[value]):flatmap(values=>values):map(value=>value:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json`,
		`labels:items:map(pair=>pair[1]):map(value=>{"payload":value}):map(object=>object:get("payload"):type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json`,
		`labels:items:map(pair=>pair[1]):map(value=>{"payload":value}):map(object=>object.payload:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json`,
		`labels:items:map(pair=>pair[1]):map(value=>[value,value]):map(values=>values[-1]:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json`,
		`labels:items:map(pair=>pair[1]):map(value=>[value]):map(values=>values:[list[any]]):flatmap(values=>values):map(value=>value:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json`,
		`labels:items:map(pair=>pair[1]):map(value=>[value,null]):map(values=>values:size):all(size=>size=2)`,
		`labels:items:map(pair=>pair[1]):flatmap(value=>[value])[0]:type="string"`,
		`labels:items:map(pair=>pair[1]):flatmap(value=>[value])[1]:type="bytes"`,
		`labels:items:map(pair=>pair[1]):filter(value=>value:exists):flatmap(value=>[value])[labels:size%3-1]:type="string"`,
		`labels:items:map(pair=>pair[1]!):flatmap(value=>[value])[0]:type="string"`,
		`labels:items:map(pair=>pair[1]):foldl([], (types,value)=>types:concat([value:type])):json=["string","bytes","duration","timestamp"]:json`,
		`labels:items:map(pair=>pair[1]):foldr("",(value,types)=>value:type:concat(types))="stringbytesdurationtimestamp"`,
		`labels:items:map(pair=>pair[1]):foldl("",(last,value)=>value:type)="timestamp"`,
		`labels:items:map(pair=>pair[1]):foldl([], (values,value)=>values:concat([value])):map(value=>value:type):json=["string","bytes","duration","timestamp"]:json`,
		`labels:items:map(pair=>pair[1]):foldl({}, (acc,value)=>{"previous":acc,"value":value}).value:type="timestamp"`,
		`labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value]))=labels:items:map(pair=>pair[1])`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("events", "labels"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("events", "labels_meta")))))
		if err != nil {
			t.Fatal(source, err)
		}
		appendAggregateProfileQuery(t, &script, q, "events ORDER BY id", i)
		if i < 5 {
			expected = append(expected, true, true, false, false)
		} else if i == 11 || i == 13 || i == 17 || i == 19 {
			expected = append(expected, true, true, true, false)
		} else if i == 12 || i == 14 || i == 15 || i == 16 || i == 18 {
			expected = append(expected, true, true, false, false)
		} else {
			expected = append(expected, true, true, true, true)
		}
		for row := 1; row <= 4; row++ {
			labels = append(labels, fmt.Sprintf("query%d row%d", i, row))
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
