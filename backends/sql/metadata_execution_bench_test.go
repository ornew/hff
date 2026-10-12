//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// EXPLAIN metrics exclude fixture loading, local evaluation and transport.
// Every sample validates exact matching IDs before measuring the warmed scan.
func BenchmarkPostgreSQLMetadataExecution(b *testing.B) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		b.Fatal("set HFF_POSTGRES_CONTAINER to the dedicated test database")
	}
	e := metadataBenchmarkEngine(b)
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		mode := "NativeJSON"
		if format == hffsql.JSONB {
			mode = "JSONBMetadata"
		}
		b.Run(mode, func(b *testing.B) {
			for _, rows := range []int{100, 1000} {
				b.Run(strconv.Itoa(rows)+"Rows", func(b *testing.B) {
					for _, query := range metadataBenchmarkQueries {
						b.Run(query.name, func(b *testing.B) {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
							defer cancel()
							program, err := e.Compile(ctx, query.source)
							if err != nil {
								b.Fatal(err)
							}
							translated, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(metadataBenchmarkBinding(format)))
							if err != nil {
								b.Fatal(err)
							}
							filter, err := local.Build(ctx, program)
							if err != nil {
								b.Fatal(err)
							}
							var script strings.Builder
							script.WriteString("SET statement_timeout='60s'; SET jit=off; SET max_parallel_workers_per_gather=0; SET plan_cache_mode=force_custom_plan;\n")
							fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,doc %s,metadata jsonb);\n", format)
							matched := []int{}
							for i := 0; i < rows; i++ {
								value, raw, metadata := metadataBenchmarkFixture(i)
								yes, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"doc": value}))
								if err != nil {
									b.Fatal(err)
								}
								if yes {
									matched = append(matched, i)
								}
								fmt.Fprintf(&script, "INSERT INTO records VALUES (%d,%s::%s,%s::jsonb);\n", i, sqlFixtureString(raw), format, sqlFixtureString(metadata))
							}
							script.WriteString("ANALYZE records;\n")
							var types, literals []string
							for _, arg := range translated.Args() {
								typ, literal := sqlFixtureParameter(b, arg)
								types = append(types, typ)
								literals = append(literals, literal)
							}
							params, args := "", ""
							if len(types) > 0 {
								params = "(" + strings.Join(types, ",") + ")"
								args = "(" + strings.Join(literals, ",") + ")"
							}
							fmt.Fprintf(&script, "PREPARE validation%s AS SELECT COALESCE(json_agg(id ORDER BY id),'[]'::json) FROM records WHERE %s;\nPREPARE measured%s AS SELECT id FROM records WHERE %s;\nEXECUTE validation%s;\nEXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS) EXECUTE measured%s;\n", params, translated.Where(), params, translated.Where(), args, args)
							var planning, execution, nodes float64
							count := 0
							for b.Loop() {
								command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
								command.Stdin = strings.NewReader(script.String())
								output, err := command.CombinedOutput()
								if err != nil {
									b.Fatalf("metadata EXPLAIN: %v\n%s", err, output)
								}
								decoder := json.NewDecoder(strings.NewReader(string(output)))
								var ids []int
								var plans []executionPlan
								var planJSON json.RawMessage
								if err := decoder.Decode(&ids); err != nil {
									b.Fatalf("matching IDs: %v\n%s", err, output)
								}
								if !reflect.DeepEqual(ids, matched) {
									b.Fatalf("SQL/local IDs differ: got %v, want %v", ids, matched)
								}
								if err := decoder.Decode(&planJSON); err != nil {
									b.Fatalf("EXPLAIN JSON: %v\n%s", err, output)
								}
								if err := json.Unmarshal(planJSON, &plans); err != nil {
									b.Fatal(err)
								}
								if len(plans) != 1 || plans[0].Plan.ActualRows != float64(len(matched)) {
									b.Fatalf("plan cardinality mismatch: %s", output)
								}
								if count == 0 {
									if dir := os.Getenv("HFF_POSTGRES_PLAN_DIR"); dir != "" {
										if err := os.MkdirAll(dir, 0755); err != nil {
											b.Fatal(err)
										}
										base := filepath.Join(dir, fmt.Sprintf("Metadata-%s-%d-%s", mode, rows, query.name))
										if err := os.WriteFile(base+".json", planJSON, 0644); err != nil {
											b.Fatal(err)
										}
										idsJSON, err := json.Marshal(ids)
										if err != nil {
											b.Fatal(err)
										}
										if err := os.WriteFile(base+".ids.json", idsJSON, 0644); err != nil {
											b.Fatal(err)
										}
										if err := os.WriteFile(base+".sql", []byte(script.String()), 0644); err != nil {
											b.Fatal(err)
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
							b.ReportMetric(float64(len(translated.Where())), "sql-bytes")
						})
					}
				})
			}
		})
	}
}
