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

func TestPostgreSQLFloatAggregateProfileConformance(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, digits := range []int{1, 3} {
		fmt.Fprintf(&script, "SET extra_float_digits=%d;\n", digits)
		for _, tc := range conformance.FloatAggregateCases() {
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(tc.Name, err)
			}
			p, err := e.Compile(ctx, tc.PostgreSQLSource)
			if err != nil {
				t.Fatal(tc.Name, err)
			}
			items, _ := tc.Record.Lookup("items")
			want, _ := tc.Record.Lookup("expected")
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...),
					hffsql.BindJSON(hff.Path{"expected"}, hffsql.Column("records", "expected"), hffsql.JSON),
					hffsql.Bind(hff.Path{"rate"}, hffsql.Column("records", "rate")),
				))
				if err != nil {
					t.Fatal(tc.Name, err)
				}
				from := fmt.Sprintf("(VALUES (%s::%s,%s::jsonb,%s::json,1.25::double precision)) records(items,metadata,expected,rate)",
					sqlFixtureString(metadataJSONFixture(t, items)), format, sqlFixtureString(pairTypeMetadata(t, items)),
					sqlFixtureString(metadataJSONFixture(t, want)))
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, true)
				labels = append(labels, fmt.Sprintf("digits%d/%s/%s", digits, format, tc.Name))
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("verified %d backend-specific JSON/JSONB aggregate expectations", len(expected))
}

func TestPostgreSQLRelationFloatAverageProfileConformance(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_profile_average_items(parent_id bigint,value double precision);\n")
	var expected []bool
	var labels []string
	for row, tc := range conformance.ScalarFloatAverageCases() {
		e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(tc.Name, err)
		}
		p, err := e.Compile(ctx, tc.PostgreSQLSource)
		if err != nil {
			t.Fatal(tc.Name, err)
		}
		relation, err := hffsql.NewRelation(
			hffsql.WithRelationSource(hffsql.Column("hff_profile_average_items"), "item"),
			hffsql.WithCorrelation(hffsql.Equal(
				hffsql.ColumnValue(hffsql.Column("item", "parent_id"), schema.IntType),
				hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType),
			)),
			hffsql.WithRelationItem(tc.Element, hffsql.Bind(nil, hffsql.Column("item", "value"))),
			hffsql.WithRelationState(hffsql.Boolean(true), hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "is_null"), schema.BoolType))),
		)
		if err != nil {
			t.Fatal(tc.Name, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindRelation(hff.Path{"items"}, relation),
			hffsql.BindJSON(hff.Path{"expected"}, hffsql.Column("records", "expected"), hffsql.JSON),
			hffsql.Bind(hff.Path{"rate"}, hffsql.Column("records", "rate")),
		))
		if err != nil {
			t.Fatal(tc.Name, err)
		}
		input, _ := tc.Record.Lookup("items")
		values, _ := input.AsList()
		for _, value := range values {
			fmt.Fprintf(&script, "INSERT INTO hff_profile_average_items VALUES(%d,%s);\n", row, sqlFixtureValue(t, value, "double precision"))
		}
		want, _ := tc.Record.Lookup("expected")
		from := fmt.Sprintf("(VALUES (%d::bigint,%t,%s::json,1.25::double precision)) records(id,is_null,expected,rate)",
			row, input.Kind() == hff.NullKind, sqlFixtureString(metadataJSONFixture(t, want)))
		for _, digits := range []int{1, 3} {
			fmt.Fprintf(&script, "SET extra_float_digits=%d;\n", digits)
			appendAggregateProfileQuery(t, &script, q, from, len(expected))
			expected = append(expected, true)
			labels = append(labels, fmt.Sprintf("digits%d/relation/%s", digits, tc.Name))
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("verified %d backend-specific relation average expectations", len(expected))
}

func appendAggregateProfileQuery(t *testing.T, script *strings.Builder, q *hffsql.Query, from string, index int) {
	t.Helper()
	var types, args []string
	for _, arg := range q.Args() {
		typ, literal := sqlFixtureParameter(t, arg)
		types = append(types, typ)
		args = append(args, literal)
	}
	params, arguments := "", ""
	if len(types) > 0 {
		params, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
	}
	name := fmt.Sprintf("profile_aggregate%d", index)
	fmt.Fprintf(script, "PREPARE %s%s AS SELECT %s FROM %s; EXECUTE %s%s; DEALLOCATE %s;\n", name, params, q.Where(), from, name, arguments, name)
}
