//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

// Server-reported EXPLAIN times exclude Docker/psql transport and fixture load.
// This controlled scan workload establishes a baseline, not an index claim.
func BenchmarkPostgreSQLJSONExecution(b *testing.B) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		b.Fatal("set HFF_POSTGRES_CONTAINER to the dedicated test database")
	}
	e := compositeEngine(b)
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		name := "JSON"
		storage := "json"
		if format == hffsql.JSONB {
			name = "JSONB"
			storage = "jsonb"
		}
		b.Run(name, func(b *testing.B) {
			for _, rows := range []int{100, 1000} {
				b.Run(strconv.Itoa(rows)+"Rows", func(b *testing.B) {
					for _, query := range []struct{ name, source string }{{"Scalar", `profile.age>=18`}, {"Any", `profile.people:any(x=>x.name="Alice")`}, {"Fold", `profile.scores:foldl(0,(acc,x)=>acc+x)>=100`}} {
						b.Run(query.name, func(b *testing.B) {
							ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
							defer cancel()
							p, err := e.Compile(ctx, query.source)
							if err != nil {
								b.Fatal(err)
							}
							q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), format)))
							if err != nil {
								b.Fatal(err)
							}
							f, err := local.Build(ctx, p)
							if err != nil {
								b.Fatal(err)
							}
							var script strings.Builder
							script.WriteString("SET statement_timeout='30s'; SET jit=off; SET max_parallel_workers_per_gather=0; SET plan_cache_mode=force_custom_plan;\n")
							fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,profile %s);\n", storage)
							matched := 0
							for i := 0; i < rows; i++ {
								v, raw := executionFixture(i)
								ok, err := f.Match(ctx, hff.Object(map[string]hff.Value{"profile": v}))
								if err != nil {
									b.Fatal(err)
								}
								if ok {
									matched++
								}
								fmt.Fprintf(&script, "INSERT INTO records VALUES (%d,%s::%s);\n", i, sqlFixtureString(raw), storage)
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
									b.Fatalf("SQL/local cardinality mismatch: %s, expected %d", out, matched)
								}
								if count == 0 {
									if dir := os.Getenv("HFF_POSTGRES_PLAN_DIR"); dir != "" {
										if err := os.MkdirAll(dir, 0755); err != nil {
											b.Fatal(err)
										}
										base := filepath.Join(dir, fmt.Sprintf("%s-%d-%s", name, rows, query.name))
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
							b.ReportMetric(float64(rows), "input-rows")
							b.ReportMetric(float64(matched), "matched-rows")
							b.ReportMetric(float64(len(q.Where())), "sql-bytes")
						})
					}
				})
			}
		})
	}
}

type executionPlan struct {
	Plan      executionNode `json:"Plan"`
	Planning  float64       `json:"Planning Time"`
	Execution float64       `json:"Execution Time"`
}
type executionNode struct {
	ActualRows float64         `json:"Actual Rows"`
	Plans      []executionNode `json:"Plans"`
}

func planNodeCount(n executionNode) int {
	count := 1
	for _, child := range n.Plans {
		count += planNodeCount(child)
	}
	return count
}
func executionFixture(i int) (hff.Value, string) {
	if i%13 == 0 {
		return hff.Null(), "null"
	}
	name := "Bob"
	if i%2 == 0 {
		name = "Alice"
	}
	age := int64(i % 50)
	values := map[string]hff.Value{"name": hff.String(name), "age": hff.Int(age), "father": hff.Null(), "scores": hff.List(hff.Int(30), hff.Int(40), hff.Int(age)), "factors": hff.Null(), "people": hff.List(hff.Object(map[string]hff.Value{"name": hff.String(name)}), hff.Null(), hff.Object(map[string]hff.Value{"name": hff.String("Bob")}))}
	raw := fmt.Sprintf(`{"name":%q,"age":%d,"father":null,"scores":[30,40,%d],"factors":null,"people":[{"name":%q},null,{"name":"Bob"}]}`, name, age, age, name)
	return hff.Object(values), raw
}
