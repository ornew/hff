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

func TestPostgreSQLCollectionEmptinessMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.List("tags", schema.StringType)),
		schema.Nullable(schema.Map("labels", schema.NullableType(schema.StringType))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		tags, labels hff.Value
		tagsSQL      string
		labelsSQL    string
	}{
		{hff.Null(), hff.Null(), "NULL::jsonb", "NULL::jsonb"},
		{hff.Null(), hff.Null(), "'null'::jsonb", "'null'::jsonb"},
		{hff.List(), hff.Object(map[string]hff.Value{}), "'[]'::jsonb", "'{}'::jsonb"},
		{hff.List(hff.String("x")), hff.Object(map[string]hff.Value{"a": hff.Null()}), "'[\"x\"]'::jsonb", "'{\"a\":null}'::jsonb"},
		{hff.List(hff.String("x"), hff.String("y")), hff.Object(map[string]hff.Value{"a": hff.String("x"), "b": hff.String("y")}), "'[\"x\",\"y\"]'::jsonb", "'{\"a\":\"x\",\"b\":\"y\"}'::jsonb"},
	}
	sources := []string{
		`tags=[]`, `tags!=[]`, `tags:size=0`, `tags:size>0`, `tags:size=2`, `tags`,
		`!(tags=[])`, `labels={}`, `labels!={}`, `labels:size=0`, `labels:size>0`, `labels`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for i, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSONB),
		))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, value)
		}
		var values []string
		for ri, row := range rows {
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"tags": row.tags, "labels": row.labels}))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, match)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			values = append(values, "("+row.tagsSQL+","+row.labelsSQL+")")
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE collection_%d%s AS SELECT %s FROM (VALUES %s) records(tags,labels); EXECUTE collection_%d%s;\n",
			i, parameters, query.Where(), strings.Join(values, ","), i, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), output)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: PostgreSQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}
