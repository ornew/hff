//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

// Measure server planning and execution separately from Docker transport and
// fixture construction. Parent correlation has an index; no latency guarantee
// or cross-backend comparison is inferred from this controlled workload.
func BenchmarkPostgreSQLRelationExecution(b *testing.B) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		b.Fatal("set HFF_POSTGRES_CONTAINER to the dedicated test database")
	}
	e := relationEngine(b)
	for _, parents := range []int{100, 1000} {
		for _, fanout := range []int{4, 32} {
			b.Run(fmt.Sprintf("%dParents/%dItems", parents, fanout), func(b *testing.B) {
				for _, query := range []struct{ name, source string }{
					{"Any", `items:any(x=>x.price>30)`}, {"MapAny", `items:map(x=>x.price):any(price=>price>30)`}, {"All", `items:all(x=>x.active)`},
					{"Index", `items[0].price>30`}, {"Fold", `items:foldl(0,(acc,x)=>acc+x.price)>100`},
				} {
					b.Run(query.name, func(b *testing.B) {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
						defer cancel()
						p, err := e.Compile(ctx, query.source)
						if err != nil {
							b.Fatal(err)
						}
						q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(relationBinding(b, true)))
						if err != nil {
							b.Fatal(err)
						}
						f, err := local.Build(ctx, p)
						if err != nil {
							b.Fatal(err)
						}
						matched := 0
						for parent := 0; parent < parents; parent++ {
							ok, err := f.Match(ctx, relationExecutionFixture(parent, fanout))
							if err != nil {
								b.Fatal(err)
							}
							if ok {
								matched++
							}
						}
						var script strings.Builder
						script.WriteString("SET statement_timeout='30s'; SET jit=off; SET max_parallel_workers_per_gather=0; SET plan_cache_mode=force_custom_plan;\n")
						fmt.Fprintf(&script, "CREATE TEMP TABLE records AS SELECT id FROM generate_series(0,%d) AS parents(id); CREATE TEMP TABLE hff_related AS SELECT (parents.id*%d+items.ordinal)::bigint AS id, parents.id AS parent_id, ((parents.id+items.ordinal)%%50)::bigint AS price, (items.ordinal%%3<>0) AS active, (CASE WHEN (parents.id+items.ordinal)%%2=0 THEN '[\"auth\"]' ELSE '[\"dev\"]' END)::json AS tags FROM records AS parents CROSS JOIN generate_series(0,%d) AS items(ordinal); CREATE INDEX ON hff_related(parent_id); ANALYZE records; ANALYZE hff_related;\n", parents-1, fanout, fanout-1)
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
						fmt.Fprintf(&script, "PREPARE measured%s AS SELECT id FROM records WHERE %s;\nEXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS) EXECUTE measured%s;\n", params, q.Where(), args)
						var planning, execution, nodes float64
						count := 0
						for b.Loop() {
							cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
							cmd.Stdin = strings.NewReader(script.String())
							out, err := cmd.CombinedOutput()
							if err != nil {
								b.Fatalf("EXPLAIN: %v\n%s", err, out)
							}
							var plans []executionPlan
							if err := json.Unmarshal(out, &plans); err != nil {
								b.Fatalf("EXPLAIN JSON: %v\n%s", err, out)
							}
							if len(plans) != 1 || plans[0].Plan.ActualRows != float64(matched) {
								b.Fatalf("SQL/local cardinality mismatch: %s; expected %d", out, matched)
							}
							if count == 0 {
								if dir := os.Getenv("HFF_POSTGRES_PLAN_DIR"); dir != "" {
									if err := os.MkdirAll(dir, 0755); err != nil {
										b.Fatal(err)
									}
									base := filepath.Join(dir, fmt.Sprintf("Relation-%d-%d-%s", parents, fanout, query.name))
									if err := os.WriteFile(base+".json", out, 0644); err != nil {
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
						b.ReportMetric(float64(parents), "input-rows")
						b.ReportMetric(float64(parents*fanout), "related-rows")
						b.ReportMetric(float64(matched), "matched-rows")
						b.ReportMetric(float64(len(q.Where())), "sql-bytes")
					})
				}
			})
		}
	}
}
func relationExecutionFixture(parent, fanout int) hff.Value {
	items := make([]hff.Value, fanout)
	for ordinal := range items {
		tag := "dev"
		if (parent+ordinal)%2 == 0 {
			tag = "auth"
		}
		items[ordinal] = hff.Object(map[string]hff.Value{"price": hff.Int(int64((parent + ordinal) % 50)), "active": hff.Bool(ordinal%3 != 0), "tags": hff.List(hff.String(tag))})
	}
	return hff.Object(map[string]hff.Value{"items": hff.List(items...)})
}
