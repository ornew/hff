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
)

// Omitted parameter declarations exercise the normal driver inference path;
// explicit declarations alone can hide an unused placeholder in Args().
func appendNullArgumentQuery(t *testing.T, script *strings.Builder, q *hffsql.Query, from string, index int, typed bool) {
	t.Helper()
	var types, args []string
	for _, arg := range q.Args() {
		typ, literal := sqlFixtureParameter(t, arg)
		types = append(types, typ)
		args = append(args, literal)
	}
	params, values := "", ""
	if typed && len(types) > 0 {
		params = "(" + strings.Join(types, ",") + ")"
	}
	if len(args) > 0 {
		values = "(" + strings.Join(args, ",") + ")"
	}
	name := fmt.Sprintf("null_arguments_%d", index)
	fmt.Fprintf(script, "PREPARE %s%s AS SELECT %s FROM %s; EXECUTE %s%s; DEALLOCATE %s;\n", name, params, q.Where(), from, name, values, name)
}

func TestPostgreSQLNullBinaryArgumentsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e := nullArgumentEngine(t)
	type fixture struct {
		json string
		data hff.Value
		age  int64
	}
	fixtures := []fixture{
		{`'{"nil":null,"name":"Alice"}'`, hff.Object(map[string]hff.Value{"nil": hff.Null(), "name": hff.String("Alice")}), 20},
		{`'{"name":"Bob"}'`, hff.Object(map[string]hff.Value{"name": hff.String("Bob")}), 20},
		{`NULL`, hff.Null(), 20},
		{`'{"nil":null,"name":""}'`, hff.Object(map[string]hff.Value{"nil": hff.Null(), "name": hff.String("")}), 0},
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, source := range nullArgumentSources() {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(ctx, p, nullArgumentOptions(format)...)
			if err != nil {
				t.Fatal(source, format, err)
			}
			requireConcatArgsUsed(t, q)
			for _, record := range fixtures {
				want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"labels": record.data, "age": hff.Int(record.age)}))
				if err != nil {
					t.Fatal(source, err)
				}
				from := fmt.Sprintf("(VALUES (%s::%s,%d::bigint)) records(labels,age)", record.json, format, record.age)
				for _, typed := range []bool{false, true} {
					appendNullArgumentQuery(t, &script, q, from, len(expected), typed)
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s %s %s typed=%t", format, source, record.json, typed))
				}
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("verified %d typed and inferred-parameter results", len(expected))
}
