//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLPipelineStageRetainsNullExtensionCarrier(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, method := range []string{"bool", "round", "duration", "timestamp"} {
		q := pipelineNullCarrierQuery(t, method)
		for _, document := range []string{`{"app":null}`, `{"app":"x","z":null}`, `{}`} {
			appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(document)+"::json)) records(labels)", len(expected))
			expected = append(expected, true)
			labels = append(labels, method+" "+document)
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

// Inspect plans without executing or timing their expressions. A row-local
// kernel must sit behind the active-row gate; captured record work keeps its
// existing hoisting opportunities instead of being forced into every row.
func TestPostgreSQLPipelineStagePlanGatesCorrelatedWork(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.NullableType(schema.StringType)), schema.String("message"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		rowLocal   bool
	}{
		{"row-local", `v:matches("^expensive")`, true},
		{"captured-record", `message:matches("^expensive")`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := e.Compile(ctx, `labels:items:map(p=>p[1]):filter(v=>v:startswith("keep")):map(v=>`+tc.body+`):any(v=>v)`)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.Bind(hff.Path{"message"}, hffsql.Column("records", "message"))))
			if err != nil {
				t.Fatal(err)
			}
			var statement strings.Builder
			appendAggregateProfileQuery(t, &statement, q, "records", 0)
			script := `CREATE TEMP TABLE records(labels json,message text);` + strings.Replace(statement.String(), "EXECUTE ", "EXPLAIN (VERBOSE,COSTS OFF,FORMAT JSON) EXECUTE ", 1)
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script)
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var plans []struct{ Plan pipelineStagePlanNode }
			if err := json.Unmarshal(out, &plans); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if len(plans) != 1 {
				t.Fatalf("got %d plans", len(plans))
			}
			guarded, unguarded := pipelineStagePlanKernelLeaves(plans[0].Plan, false)
			if tc.rowLocal && (guarded == 0 || unguarded != 0) {
				t.Fatalf("row kernel leaves guarded=%d unguarded=%d", guarded, unguarded)
			}
			if !tc.rowLocal && unguarded == 0 {
				t.Fatal("captured record kernel lost its outer placement")
			}
			t.Logf("kernel plan leaves: guarded=%d unguarded=%d; no execution or timing", guarded, unguarded)
		})
	}
}

type pipelineStagePlanNode struct {
	OneTime string `json:"One-Time Filter"`
	Output  []string
	Plans   []pipelineStagePlanNode
}

func pipelineStagePlanKernelLeaves(n pipelineStagePlanNode, gated bool) (guarded, unguarded int) {
	gated = gated || n.OneTime != ""
	if len(n.Plans) == 0 {
		for _, output := range n.Output {
			// VERBOSE expands references to earlier output columns. A later
			// Boolean projection can therefore print the regex again without
			// evaluating it. Identify this fixture's direct kernel result,
			// rather than counting every occurrence in deparsed expressions.
			if !strings.HasPrefix(strings.TrimLeft(strings.TrimSpace(output), "("), "regexp_like(") {
				continue
			}
			if gated {
				guarded++
			} else {
				unguarded++
			}
		}
	}
	for _, child := range n.Plans {
		g, u := pipelineStagePlanKernelLeaves(child, gated)
		guarded += g
		unguarded += u
	}
	return
}
