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
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
)

func TestPostgreSQLSharedCompositeConformance(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type fixture struct{ native, json string }
	fixtures := map[string]fixture{
		"null_parent":       {"NULL::hff_shared_profile", "null"},
		"null_child":        {"ROW(NULL,NULL)::hff_shared_profile", `{"father":null,"scores":null}`},
		"null_name":         {"ROW(ROW(NULL)::hff_shared_child,NULL)::hff_shared_profile", `{"father":{"name":null},"scores":null}`},
		"empty_name_exists": {"ROW(ROW('')::hff_shared_child,NULL)::hff_shared_profile", `{"father":{"name":""},"scores":null}`},
		"null_array":        {"ROW(NULL,NULL)::hff_shared_profile", `{"father":null,"scores":null}`},
		"empty_array":       {"ROW(NULL,ARRAY[]::bigint[])::hff_shared_profile", `{"father":null,"scores":[]}`},
		// A non-default SQL lower bound must not change HFF's zero-based index.
		"last_integer": {"ROW(NULL,'[4:5]={NULL,2}'::bigint[])::hff_shared_profile", `{"father":null,"scores":[null,2]}`},
		"last_null":    {"ROW(NULL,ARRAY[2::bigint,NULL])::hff_shared_profile", `{"father":null,"scores":[2,null]}`},
	}
	var script strings.Builder
	// Roll back fixture types even when ON_ERROR_STOP closes the connection.
	script.WriteString("BEGIN; SET standard_conforming_strings=on; CREATE TYPE hff_shared_child AS (name text); CREATE TYPE hff_shared_profile AS (father hff_shared_child,scores bigint[]);\n")
	var expected, descriptions []string
	for _, storage := range []string{"native", "json", "jsonb"} {
		for _, tc := range conformance.CompositeCases() {
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(ctx, tc.Source)
			if err != nil {
				t.Fatal(tc.Name, err)
			}
			binding := hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))
			if storage != "native" {
				format := hffsql.JSON
				if storage == "jsonb" {
					format = hffsql.JSONB
				}
				binding = hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), format)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil {
				t.Fatal(storage, tc.Name, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range tc.Records {
				matched, err := filter.Match(ctx, row.Value)
				if err != nil || matched != row.Match {
					t.Fatalf("local %s/%s: %v %v", tc.Name, row.Name, matched, err)
				}
				data, ok := fixtures[row.Name]
				if !ok {
					t.Fatal("new shared composite record needs SQL fixture:", row.Name)
				}
				value := data.native
				if storage != "native" {
					value = sqlFixtureString(data.json) + "::" + storage
				}
				name := fmt.Sprintf("shared_composite_%d", len(expected))
				var types, literals []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					literals = append(literals, literal)
				}
				parameters, execution := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					execution = "(" + strings.Join(literals, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (SELECT %s AS profile) AS records; EXECUTE %s%s; DEALLOCATE %s;\n", name, parameters, q.Where(), value, name, execution, name)
				want := "f"
				if row.Match {
					want = "t"
				}
				expected = append(expected, want)
				descriptions = append(descriptions, storage+"/"+tc.Name+"/"+row.Name)
			}
		}
	}
	script.WriteString("ROLLBACK;\n")
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL shared composite conformance: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results, expected %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("%s: SQL=%s expected=%s", descriptions[i], actual[i], want)
		}
	}
	t.Logf("verified %d shared composite comparisons", len(expected))
}
