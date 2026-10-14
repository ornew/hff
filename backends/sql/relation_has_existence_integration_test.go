//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

func TestPostgreSQLRelationHasEndIndicesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Optional(schema.Nullable(schema.List("items", schema.NullableType(schema.IntType)))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("hff_end_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "parent_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType),
		)),
		hffsql.WithRelationState(
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "items_present"), schema.BoolType)),
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "items_null"), schema.BoolType)),
		),
		hffsql.WithRelationItem(schema.NullableType(schema.IntType), hffsql.Bind(nil, hffsql.Column("item", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	records := []hff.Value{
		hff.Object(map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Int(2))}),
		hff.Object(map[string]hff.Value{"items": hff.List()}),
		hff.Object(map[string]hff.Value{"items": hff.Null()}),
		hff.Object(nil),
		hff.Object(map[string]hff.Value{"items": hff.List(hff.Int(3))}),
		hff.Object(map[string]hff.Value{"items": hff.List(hff.Null())}),
	}
	sources := []string{
		`items:has(0)`, `items:has(-1)`, `!items:has(0)`,
		`items:has(0):type="null"`, `items:has(-1):type="null"`,
		`items:has(1)`, `items:has(-2)`, `items:has(1.5):type="null"`,
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_end_parents(id bigint,items_present boolean,items_null boolean); ")
	script.WriteString("INSERT INTO hff_end_parents VALUES (1,true,false),(2,true,false),(3,true,true),(4,false,false),(5,true,false),(6,true,false); ")
	script.WriteString("CREATE TEMP TABLE hff_end_items(parent_id bigint,value bigint); ")
	script.WriteString("INSERT INTO hff_end_items VALUES (1,1),(1,2),(5,3),(6,NULL); ")
	var expected []bool
	var labels []string
	for index, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
		if err != nil {
			t.Fatal(source, err)
		}
		if source == `items:has(0)` || source == `items:has(-1)` {
			if !strings.Contains(query.Where(), "EXISTS (SELECT 1") || strings.Contains(query.Where(), "count(*)") {
				t.Fatalf("%s did not lower to existence: %s", source, query.Where())
			}
		}
		if (source == `items:has(1)` || source == `items:has(-2)`) && !strings.Contains(query.Where(), "count(*)") {
			t.Fatalf("non-end index lost cardinality check: %s", query.Where())
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE relation_end_%d%s AS SELECT COALESCE((%s)::text,'n') FROM hff_end_parents AS records ORDER BY records.id; EXECUTE relation_end_%d%s;\n",
			index, parameters, query.Where(), index, arguments)
		for i, record := range records {
			want, err := filter.Match(ctx, record)
			if err != nil {
				t.Fatal(source, i, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s fixture %d", source, i))
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if result == "n" || (result == "true") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}
