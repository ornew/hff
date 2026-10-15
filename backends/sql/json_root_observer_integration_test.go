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

func TestPostgreSQLRootObserversMatchLocal(t *testing.T) {
	runPostgreSQLProjectionSources(t, rootObserverSources(), rootObserverCallbackSources())
}

func TestPostgreSQLRootObserversPrepare(t *testing.T) {
	runPostgreSQLProjectionPrepare(t, rootObserverSources(), rootObserverCallbackSources())
}

func TestPostgreSQLRootObserverFloatGuardChains(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, withObject := range []bool{false, true} {
		t.Run(fmt.Sprintf("object=%t", withObject), func(t *testing.T) {
			typ := schema.ListOf(schema.AnyType)
			if withObject {
				typ = schema.Union(typ, schema.Struct(schema.Field("bad", schema.Union(schema.StringType, schema.BytesType))))
			}
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			var script strings.Builder
			var expected []bool
			var labels []string
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				for _, token := range []string{"1e-400", "1e400", "1.0"} {
					invalid := token != "1.0"
					for _, tc := range []struct {
						source string
						want   bool
					}{
						{`data:[list[float]]:[list[null]]:exists`, invalid},
						{`data:[list[float]]:json="[null]"`, invalid},
						{`data:[list[float]]:map(v=>v:type):json="[\"null\"]"`, invalid},
						{`data:[list[float]]:[list[float]]:exists`, !invalid},
						{`data:[list[float]]:[list[null]]:json="[null]"`, invalid},
						{`data:[list[float]]:[list[null]]:count=1`, invalid},
					} {
						p, err := e.Compile(ctx, tc.source)
						if err != nil {
							t.Fatal(tc.source, err)
						}
						q, err := hffsql.Build(ctx, p, unionProjectionOptions(format, format == hffsql.JSONB)...)
						if err != nil {
							t.Fatal(tc.source, err)
						}
						from := fmt.Sprintf("(VALUES ('[%s]'::%s,'{\"version\":1,\"types\":{\"/0\":{\"type\":\"float\"}}}'::jsonb)) records(data,metadata)", token, format)
						appendAggregateProfileQuery(t, &script, q, from, len(expected))
						expected = append(expected, tc.want)
						labels = append(labels, fmt.Sprintf("%s %s %s", format, token, tc.source))
					}
				}
			}
			t.Logf("%d Float guard-chain results", len(expected))
			runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
		})
	}
}

func TestPostgreSQLRootObserverSelectiveGuards(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	typ := schema.Union(schema.MapOf(schema.AnyType), schema.ListOf(schema.AnyType), schema.Struct(schema.Field("bad", schema.Union(schema.StringType, schema.BytesType))))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, token := range []string{"1e-400", "1e400", "1.0"} {
			for _, tc := range []struct{ source, raw, pointer string }{
				{`data:[list[any]][0]:[float]=null`, `[` + token + `]`, "/0"},
				{`data:[list[any]|list[float]][0]:[float]=null`, `[` + token + `]`, "/0"},
				{`data:[object]:get("n"):[float]=null`, `{"n":` + token + `}`, "/n"},
				{`data:[object|list[float]]:[map[float]]:[map[null]]:exists`, `{"n":` + token + `}`, "/n"},
				{`data:[object|list[float]]:[list[null]]:exists`, `[` + token + `]`, "/0"},
				{`data:[object|list[any]]:[map[float]]:[map[null]]:exists`, `{"n":` + token + `}`, "/n"},
				{`data:[map[list[any]]|list[float]]:get("nested")[0]:[float]=null`, `{"nested":[` + token + `]}`, "/nested/0"},
			} {
				p, err := e.Compile(ctx, tc.source)
				if err != nil {
					t.Fatal(tc.source, err)
				}
				q, err := hffsql.Build(ctx, p, unionProjectionOptions(format, format == hffsql.JSONB)...)
				if err != nil {
					t.Fatal(tc.source, err)
				}
				metadata := `{"version":1,"types":{"` + tc.pointer + `":{"type":"float"}}}`
				from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb)) records(data,metadata)", sqlFixtureString(tc.raw), format, sqlFixtureString(metadata))
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, token != "1.0")
				labels = append(labels, fmt.Sprintf("%s %s %s", format, token, tc.source))
			}
		}
	}
	t.Logf("%d selective guard results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLRootObserverEmptyIntersections(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, list := range []bool{false, true} {
		typ := schema.Struct(schema.Optional(schema.Bytes("asset")), schema.Nullable(schema.Int("n")))
		values := []hff.Value{hff.Object(nil), hff.Object(map[string]hff.Value{"n": hff.Null()}), hff.Object(map[string]hff.Value{"n": hff.Int(1)}), hff.Object(map[string]hff.Value{"asset": hff.Bytes(nil)})}
		sources := []string{`data:[map[string]]:exists`, `data:[map[string]]:size=0`, `data:[map[string]]:json="{}"`}
		if list {
			typ = schema.ListOf(schema.IntType)
			values = []hff.Value{hff.List(), hff.List(hff.Int(1))}
			sources = []string{`data:[list[string]]:exists`, `data:[list[string]]:size=0`, `data:[list[string]]:json="[]"`}
		}
		values = append(values, hff.Null())
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", typ)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
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
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(ctx, p, unionProjectionOptions(format, false)...)
				if err != nil {
					t.Fatal(source, err)
				}
				for i, value := range values {
					want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": value}))
					if err != nil || want != (i == 0) {
						t.Fatal(source, i, want, err)
					}
					from := "(VALUES (" + sqlFixtureString(taggedCollectionJSON(t, value)) + "::" + string(format) + ")) records(data)"
					appendAggregateProfileQuery(t, &script, q, from, len(expected))
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s %s row%d", format, source, i))
				}
			}
		}
		t.Logf("%d empty/missing intersection results", len(expected))
		runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	}
}

func TestPostgreSQLRootObserverCookbook(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	objectType := schema.Struct(schema.String("app"), schema.Field("opaque", schema.Union(schema.StringType, schema.BytesType)))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Union(objectType, schema.IntType, schema.ListOf(schema.BytesType))))), engine.WithCapabilities(capabilities.Standard()))
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
	records := []hff.Value{hff.Object(map[string]hff.Value{"app": hff.String("auth"), "opaque": hff.String("YQ==")}), hff.Object(map[string]hff.Value{"app": hff.String("billing"), "opaque": hff.Bytes([]byte("b"))}), hff.Int(0), hff.List(hff.Bytes([]byte("a")))}
	var expected []bool
	var labels []string
	for i, tc := range []struct {
		source string
		rows   [4]bool
	}{
		{`labels:type="object"`, [4]bool{true, true, false, false}},
		{`labels:[object].app="auth"`, [4]bool{true, false, false, false}},
		{`labels:[map[any]]:get("app")="auth"`, [4]bool{true, false, false, false}},
		{`labels:[int]=0`, [4]bool{false, false, true, false}},
		{`labels:[list[bytes]]:size=1`, [4]bool{false, false, false, true}},
	} {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("projection_events", "labels"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		appendAggregateProfileQuery(t, &script, q, "projection_events ORDER BY id", i)
		for j, record := range records {
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"labels": record}))
			if err != nil || want != tc.rows[j] {
				t.Fatal(tc.source, j, want, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s row%d", tc.source, j+1))
		}
	}
	t.Logf("%d cookbook results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
