//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

func BenchmarkPostgreSQLDurationExecution(b *testing.B) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		b.Fatal("set HFF_POSTGRES_CONTAINER")
	}
	for _, format := range []hffsql.JSONFormat{"", hffsql.JSON, hffsql.JSONB} {
		label := string(format)
		physical := string(format)
		if format == "" {
			label, physical = "native", "text"
		}
		for _, w := range durationWorkloads() {
			rowCounts := []int{2, 25}
			if w.digits == 20000 {
				rowCounts = []int{1}
			}
			for _, rows := range rowCounts {
				b.Run(label+"/"+strconv.Itoa(rows)+"Rows/"+w.name, func(b *testing.B) {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					e := durationEngine(b)
					p, err := e.Compile(ctx, w.source)
					if err != nil {
						b.Fatal(err)
					}
					q, err := hffsql.Build(ctx, p, durationOptions(format)...)
					if err != nil {
						b.Fatal(err)
					}
					filter, err := local.Build(ctx, p, local.WithLimits(hff.Limits{MaxSteps: math.MaxInt}))
					if err != nil {
						b.Fatal(err)
					}
					var script strings.Builder
					fmt.Fprintf(&script, "SET statement_timeout='60s'; SET jit=off; SET max_parallel_workers_per_gather=0; SET plan_cache_mode=force_custom_plan; CREATE TEMP TABLE records(id integer,value %s);\n", physical)
					matched := []int{}
					for i := 0; i < rows; i++ {
						v := durationFixture(w, i)
						yes, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": v}))
						if err != nil {
							b.Fatal(err)
						}
						if yes {
							matched = append(matched, i)
						}
						literal := sqlFixtureString(metadataJSONFixture(b, v)) + "::" + physical
						if format == "" {
							literal = sqlFixtureValue(b, v, "text")
						}
						fmt.Fprintf(&script, "INSERT INTO records VALUES (%d,%s);\n", i, literal)
					}
					script.WriteString("ANALYZE records;\n")
					var types, literals []string
					for _, arg := range q.Args() {
						typ, literal := sqlFixtureParameter(b, arg)
						types = append(types, typ)
						literals = append(literals, literal)
					}
					params, args := "", ""
					if len(types) > 0 {
						params = "(" + strings.Join(types, ",") + ")"
						args = "(" + strings.Join(literals, ",") + ")"
					}
					fmt.Fprintf(&script, "PREPARE validation%s AS SELECT COALESCE(json_agg(id ORDER BY id),'[]'::json) FROM records WHERE %s; PREPARE measured%s AS SELECT id FROM records WHERE %s; EXECUTE validation%s; EXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS) EXECUTE measured%s;\n", params, q.Where(), params, q.Where(), args, args)
					var planning, execution, nodes float64
					count := 0
					for b.Loop() {
						cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
						cmd.Stdin = strings.NewReader(script.String())
						out, err := cmd.CombinedOutput()
						if err != nil {
							b.Fatalf("%v\n%s", err, out)
						}
						decoder := json.NewDecoder(strings.NewReader(string(out)))
						var ids []int
						var raw json.RawMessage
						var plans []executionPlan
						if err := decoder.Decode(&ids); err != nil {
							b.Fatal(err)
						}
						if !reflect.DeepEqual(ids, matched) {
							b.Fatalf("SQL/local IDs differ: %v want%v", ids, matched)
						}
						if err := decoder.Decode(&raw); err != nil {
							b.Fatal(err)
						}
						if err := json.Unmarshal(raw, &plans); err != nil {
							b.Fatal(err)
						}
						if len(plans) != 1 || plans[0].Plan.ActualRows != float64(len(matched)) {
							b.Fatalf("plan cardinality mismatch: %s", raw)
						}
						if count == 0 {
							if dir := os.Getenv("HFF_POSTGRES_PLAN_DIR"); dir != "" {
								if err := os.MkdirAll(dir, 0755); err != nil {
									b.Fatal(err)
								}
								base := filepath.Join(dir, fmt.Sprintf("Duration-%s-%d-%s", label, rows, w.name))
								idsJSON, _ := json.Marshal(ids)
								for suffix, content := range map[string][]byte{".json": raw, ".ids.json": idsJSON, ".sql": []byte(script.String())} {
									if err := os.WriteFile(base+suffix, content, 0644); err != nil {
										b.Fatal(err)
									}
								}
							}
						}
						planning += plans[0].Planning
						execution += plans[0].Execution
						nodes += float64(planNodeCount(plans[0].Plan))
						count++
					}
					b.ReportMetric(0, "ns/op")
					b.ReportMetric(planning/float64(count), "planning-ms/op")
					b.ReportMetric(execution/float64(count), "execution-ms/op")
					b.ReportMetric(nodes/float64(count), "plan-nodes")
					b.ReportMetric(float64(rows), "input-rows")
					b.ReportMetric(float64(len(matched)), "matched-rows")
					b.ReportMetric(float64(len(q.Where())), "sql-bytes")
				})
			}
		}
	}
}
