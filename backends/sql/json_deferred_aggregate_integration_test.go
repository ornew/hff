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
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLDeferredAggregatesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredAggregateSources, deferredValueFixtures()...)
}

func TestPostgreSQLDeferredAggregateAnyMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredAggregateAnySources, append(itemAnyFixtures(), runtimeMembershipEdgeFixture())...)
}

func TestPostgreSQLDeferredAggregateExtremesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredAggregateExtremeSources, deferredAggregateExtremeFixture())
}

func TestPostgreSQLDeferredAggregateFloatProfile(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.AnyType), schema.Any("expected"), schema.Float("rate"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const items = `labels:items:map(p=>p[1]):foldl(["YQ==":frombase64], (a,v)=>a:concat([v]))[-1]:[list[any]|null]`
	var script strings.Builder
	var expected []bool
	var names []string
	for _, digits := range []int{1, 3} {
		fmt.Fprintf(&script, "SET extra_float_digits=%d;\n", digits)
		for _, tc := range conformance.FloatAggregateCases() {
			if !(strings.Contains(tc.Name, "/dynamic/") && (strings.HasSuffix(tc.Name, "/sum") || strings.HasSuffix(tc.Name, "/avg"))) && !strings.HasPrefix(tc.Name, "captured_rate/") {
				continue
			}
			source := strings.ReplaceAll(tc.PostgreSQLSource, "items", items)
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.Name, source, err)
			}
			input, _ := tc.Record.Lookup("items")
			want, _ := tc.Record.Lookup("expected")
			labels := hff.Object(map[string]hff.Value{"app": input})
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, options...),
					hffsql.BindJSON(hff.Path{"expected"}, hffsql.Column("records", "expected"), hffsql.JSON),
					hffsql.Bind(hff.Path{"rate"}, hffsql.Column("records", "rate")),
				))
				if err != nil {
					t.Fatal(tc.Name, source, err)
				}
				from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb,%s::json,1.25::double precision)) records(labels,metadata,expected,rate)",
					sqlFixtureString(metadataJSONFixture(t, labels)), format, sqlFixtureString(pairTypeMetadata(t, labels)), sqlFixtureString(metadataJSONFixture(t, want)))
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, true)
				names = append(names, fmt.Sprintf("digits%d/%s/%s", digits, format, tc.Name))
			}
		}
	}
	t.Logf("%d backend-specific aggregate expectations", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, names)
}

func TestPostgreSQLDeferredAggregateSkipsRejectedFloats(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const fold = `items:foldl(["YQ==":frombase64], (a,v)=>a:concat([v])):filter(v=>v:type!="bytes")`
	var script strings.Builder
	var expected []bool
	var names []string
	for _, tc := range []struct {
		input              string
		sum, avg, min, max hff.Value
	}{
		{`[1,1e-400]`, hff.Int(1), hff.Float(1), hff.Int(1), hff.Int(1)},
		{`[1e-400]`, hff.Int(0), hff.Null(), hff.Null(), hff.Null()},
		{`[1e400,1]`, hff.Int(1), hff.Float(1), hff.Int(1), hff.Int(1)},
		{`[1,2.5,1e-400]`, hff.Float(3.5), hff.Float(1.75), hff.Int(1), hff.Float(2.5)},
	} {
		for _, aggregate := range []struct {
			name string
			want hff.Value
		}{{"sum", tc.sum}, {"avg", tc.avg}, {"min", tc.min}, {"max", tc.max}} {
			for _, callback := range []string{"", "(v=>v)"} {
				source := extremeFixtureSource(t, fold+":"+aggregate.name+callback, aggregate.want)
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
				if err != nil {
					t.Fatal(source, err)
				}
				from := "(VALUES (" + sqlFixtureString(tc.input) + "::json)) records(items)"
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, true)
				names = append(names, source+" on "+tc.input)
			}
		}
	}
	t.Logf("%d rejected-Float expectations", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, names)
}

func TestPostgreSQLDeferredLengthMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredLengthSources, deferredLengthFixtures()...)
}

func TestPostgreSQLDeferredLengthRetainsNullCarrier(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for i, q := range deferredLengthNullQueries(t) {
		for _, document := range []string{`{"app":null}`, `{"app":"x","z":null}`, `{}`} {
			appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(document)+"::json)) records(labels)", len(expected))
			expected = append(expected, true)
			labels = append(labels, fmt.Sprintf("carrier%d %s", i, document))
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
