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

func TestPostgreSQLRuntimeFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLRuntimeNestedFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeNestedFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLRuntimeFoldCanonicalInputAndKeys(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Any("app"), schema.Bytes("z"))), schema.Any("key"), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	values := `labels:items:map(p=>p[1])`
	sources := []string{
		values + `:foldl([], (acc,v)=>acc:concat([v])):json=expected`,
		values + `:foldl(null,(acc,v)=>{"previous":acc,"value":v}):get(key):type="bytes"`,
		values + `:foldl(null,(acc,v)=>{"previous":acc,"value":v})[key]:type="bytes"`,
		values + `:foldl([], (acc,v)=>acc:concat([v]))[key]:type="bytes"`,
	}
	var script strings.Builder
	var expected []bool
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		var options []hffsql.JSONBindingOption
		if format == hffsql.JSONB {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
		}
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, options...),
				hffsql.BindJSON(hff.Path{"key"}, hffsql.Column("records", "key"), hffsql.JSONB),
				hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
			if err != nil {
				t.Fatal(format, source, err)
			}
			for _, input := range []struct {
				raw string
				app hff.Value
			}{
				{`{"app":"\u0061","z":"Yg=="}`, hff.String("a")},
				{`{"app": true ,"z":"Yg=="}`, hff.Bool(true)},
				{`{"app":{"\u0061":["\u0062", true]},"z":"Yg=="}`, hff.Object(map[string]hff.Value{"a": hff.List(hff.String("b"), hff.Bool(true))})},
			} {
				for _, key := range []hff.Value{hff.String("value"), hff.String("1"), hff.Int(1), hff.Float(1), hff.Float(1.5), hff.Int(-1), hff.Null()} {
					serialized := taggedCollectionJSON(t, hff.List(input.app, hff.Bytes([]byte("b"))))
					record := hff.Object(map[string]hff.Value{"labels": hff.Object(map[string]hff.Value{"app": input.app, "z": hff.Bytes([]byte("b"))}), "key": key, "expected": hff.String(serialized)})
					want, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(source, err)
					}
					from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb,'{\"version\":1,\"types\":{}}'::jsonb,%s::text)) records(labels,key,meta,expected)", sqlFixtureString(input.raw), format, sqlFixtureString(taggedCollectionJSON(t, key)), sqlFixtureString(serialized))
					appendAggregateProfileQuery(t, &script, q, from, len(expected))
					expected = append(expected, want)
					descriptions = append(descriptions, string(format)+" "+source+" "+input.raw)
				}
			}
		}
	}
	t.Logf("%d query-record results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, descriptions)
}

func TestPostgreSQLRuntimeFoldGuardDoesNotCastOtherKinds(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Any("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	source := `labels:items:map(p=>p[1]):foldl(["YQ==":frombase64],(acc,v)=>acc:concat([v])):filter(v=>v:type="number"):map(v=>v:[int]+1):exists`
	p, err := e.Compile(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		var options []hffsql.JSONBindingOption
		if format == hffsql.JSONB {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, options...)))
		if err != nil {
			t.Fatal(format, err)
		}
		for _, app := range []hff.Value{hff.Float(1e300), hff.Float(-1e300), hff.Float(1.5), hff.Int(1), hff.String("not-base64"), hff.Null()} {
			value := hff.Object(map[string]hff.Value{"app": app, "z": hff.Bytes([]byte("b"))})
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"labels": value}))
			if err != nil {
				t.Fatal(err)
			}
			from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb)) records(labels,meta)", sqlFixtureString(taggedCollectionJSON(t, value)), format, sqlFixtureString(pairTypeMetadata(t, value)))
			appendAggregateProfileQuery(t, &script, q, from, len(expected))
			expected, labels = append(expected, want), append(labels, string(format)+" "+taggedCollectionJSON(t, app))
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLRuntimeFoldIndexUsesDecodedFloat(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.String("app"), schema.Bytes("z"))), schema.Any("key"), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	source := `labels:items:map(p=>p[1]):foldl(["YQ==":frombase64],(acc,v)=>acc:concat([v]))[key]:type=expected`
	p, err := e.Compile(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []bool
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON),
			hffsql.BindJSON(hff.Path{"key"}, hffsql.Column("records", "key"), format),
			hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []struct {
			raw, want string
			value     float64
		}{
			{"0.99999999999999999", "string", 1},
			{"1.00000000000000001", "string", 1},
			{"-0.99999999999999999", "bytes", -1},
			{"2147483647.00000001", "null", 2147483647},
		} {
			labels := hff.Object(map[string]hff.Value{"app": hff.String("a"), "z": hff.Bytes([]byte("b"))})
			record := hff.Object(map[string]hff.Value{"labels": labels, "key": hff.Float(key.value), "expected": hff.String(key.want)})
			want, err := filter.Match(ctx, record)
			if err != nil || !want {
				t.Fatal(key.raw, "invalid local control", want, err)
			}
			from := fmt.Sprintf("(VALUES ('{\"app\":\"a\",\"z\":\"Yg==\"}'::json,%s::%s,%s::text)) records(labels,key,expected)", sqlFixtureString(key.raw), format, sqlFixtureString(key.want))
			appendAggregateProfileQuery(t, &script, q, from, len(expected))
			expected, descriptions = append(expected, want), append(descriptions, string(format)+" "+key.raw)
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, descriptions)
}
