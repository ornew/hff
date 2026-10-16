//go:build integration

package sql_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestPostgreSQLDisjointBytesGuardsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, fixture := range disjointBytesGuardFixtures() {
		e := disjointBytesGuardEngine(t, fixture.typ)
		for _, source := range disjointBytesGuardSources() {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(disjointBytesGuardBinding(fixture.typ)))
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			requireConcatArgsUsed(t, q)
			for i, value := range []hff.Value{fixture.value, hff.Null()} {
				want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": value}))
				if err != nil {
					t.Fatal(fixture.name, source, err)
				}
				sqlValue := fixture.sql
				if i == 1 {
					sqlValue = "CASE WHEN TRUE THEN NULL ELSE " + fixture.sql + " END"
				}
				from := `(VALUES (` + sqlValue + `)) records(value)`
				for _, typed := range []bool{false, true} {
					appendNullArgumentQuery(t, &script, q, from, len(expected), typed)
					expected = append(expected, want)
					labels = append(labels, fixture.name+" "+source+" "+sqlValue)
				}
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("%d typed/inferred guard outcomes", len(expected))
}
