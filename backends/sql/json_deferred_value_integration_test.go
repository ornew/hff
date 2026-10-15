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

func TestPostgreSQLDeferredCollectionValuesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredValueSources, deferredValueFixtures()...)
}

// Omitting PREPARE's parameter type list checks the generated statement's
// inference and placeholder arity, as a driver using Parse without OIDs does.
func TestPostgreSQLDeferredCollectionValuesPrepare(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var fixture itemRoleFixture
	for _, tc := range deferredValueFixtures() {
		if tc.name == "bytes/mixed" {
			fixture = tc
		}
	}
	e, err := engine.New(engine.WithSchema(itemRoleSchema(fixture)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	value := hff.Object(map[string]hff.Value{"app": hff.String("auth"), "z": fixture.second})
	fields := itemRoleOutputs(t, value)
	fields["labels"], fields["other"] = value, value
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		sources := append(deferredValueSources(fixture), deferredSelectionSources(fixture)...)
		sources = append(sources, deferredInputFoldSources(fixture)...)
		sources = append(sources, deferredZeroInputFoldSources(fixture)...)
		sources = append(sources, deferredFoldAccumulatorRoleSources(fixture)...)
		sources = append(sources, runtimeFoldSources(fixture)...)
		sources = append(sources, runtimeNestedFoldSources(fixture)...)
		sources = append(sources, runtimeComparisonSources(fixture)...)
		sources = append(sources, runtimeScalarComparisonSources(fixture)...)
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, false)...))
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filter.Match(ctx, hff.Object(fields))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{}
			for _, arg := range q.Args() {
				_, literal := sqlFixtureParameter(t, arg)
				args = append(args, literal)
			}
			name := fmt.Sprintf("value_prepare%d", len(expected))
			call := name
			if len(args) > 0 {
				call += "(" + strings.Join(args, ",") + ")"
			}
			raw := sqlFixtureString(taggedCollectionJSON(t, value)) + "::" + string(format)
			fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM (VALUES (%s,%s,%s)) records(labels,other,items_json); EXECUTE %s; DEALLOCATE %s;\n", name, q.Where(), raw, raw, valuesText(t, fields["items_json"]), call, name)
			expected, labels = append(expected, want), append(labels, string(format)+" "+source)
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLDeferredCollectionDispatchMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, tc := range deferredDispatchCases() {
		e, err := engine.New(engine.WithSchema(deferredDispatchSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredDispatchSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(deferredDispatchBindings(format)...))
				if err != nil {
					t.Fatal(source, err)
				}
				for _, index := range []int64{0, 1, -1, 3} {
					for _, key := range []hff.Value{tc.key, hff.Int(0)} {
						object := hff.Object(map[string]hff.Value{"app": hff.String("auth")})
						record := hff.Object(map[string]hff.Value{"labels": object, "key": key, "index": hff.Int(index)})
						want, err := filter.Match(ctx, record)
						if err != nil {
							t.Fatal(err)
						}
						from := fmt.Sprintf("(VALUES (%s::%s,%s::%s,%d::bigint,%s::jsonb)) records(labels,key,idx,key_meta)", sqlFixtureString(taggedCollectionJSON(t, object)), format, sqlFixtureString(taggedCollectionJSON(t, key)), format, index, sqlFixtureString(pairTypeMetadata(t, key)))
						appendAggregateProfileQuery(t, &script, q, from, len(expected))
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s %s index%d %s", tc.name, format, index, source))
					}
				}
			}
		}
	}
	t.Logf("%d query-record results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLDeferredStructuredLeafMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredStructuredLeafSources, deferredStructuredLeafFixture())
}
func TestPostgreSQLDeferredFirstFailureMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredFailureSources, flatmapFailureFixture())
}

func TestPostgreSQLDeferredFoldedConstructorPrepare(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType), schema.Field("unknown", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	record := hff.Object(map[string]hff.Value{"labels": hff.Object(map[string]hff.Value{"a": hff.String("b")}), "unknown": hff.Null()})
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, source := range deferredFoldedConstructorSources() {
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
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "unknown_meta")))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format), hffsql.BindJSON(hff.Path{"unknown"}, hffsql.Column("records", "unknown"), format, options...)))
			if err != nil {
				t.Fatal(err)
			}
			var args []string
			for _, arg := range q.Args() {
				_, literal := sqlFixtureParameter(t, arg)
				args = append(args, literal)
			}
			name := fmt.Sprintf("deferred_constant%d", len(expected))
			call := name
			if len(args) > 0 {
				call += "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM (VALUES ('{\"a\":\"b\"}'::%s,'null'::%s,'{\"version\":1,\"types\":{}}'::jsonb)) records(labels,unknown,unknown_meta); EXECUTE %s; DEALLOCATE %s;\n", name, q.Where(), format, format, call, name)
			expected = append(expected, want)
			labels = append(labels, source)
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
