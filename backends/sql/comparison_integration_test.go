//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestPostgreSQLSchemaGuidedEquality(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("set HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var script strings.Builder
	var expected [][]bool
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, workload := range comparisonWorkloads() {
			e := comparisonEngine(t, workload)
			var values []hff.Value
			for i := range 32 {
				values = append(values, comparisonFixture(workload.fixture, i))
			}
			right := strings.TrimPrefix(workload.source, "value=")
			sources := []string{
				workload.source, "value!=" + right, "!(" + workload.source + ")",
				"(value!)=" + right, "(value!)!=" + right,
				"(value=" + right + "):type=\"null\"",
				"(value=" + right + "):type=\"bool\"",
				"value!=null " + workload.source,
			}
			if workload.fixture == "int-list" || workload.fixture == "any-list" {
				values = append(values, hff.List())
				sources = append(sources, `value=[]`, `value=[null]`, `value=[0.0,0.0,null]`, `value=[9007199254740992.0,9007199254740992.0,null]`)
				if workload.fixture == "any-list" {
					sources = append(sources, `value=[9223372036854775807,null,null]`, `value=[9223372036854775808.0,null,null]`)
					values = append(values,
						hff.List(hff.Int(9007199254740993), hff.Float(9007199254740992), hff.Null()),
						hff.List(hff.Float(math.SmallestNonzeroFloat64), hff.Int(0), hff.Null()),
						hff.List(hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Null()),
						hff.List(hff.String("1"), hff.Bool(true), hff.Null()),
						hff.List(hff.Int(math.MaxInt64), hff.Null(), hff.Null()),
						hff.List(hff.Float(9223372036854775808), hff.Null(), hff.Null()),
					)
				}
			} else {
				sources = append(sources, `value={}`, `value={"price":null}`, `value={"price":1.0,"empty":null}`)
				values = append(values, hff.Object(map[string]hff.Value{"price": hff.Null()}))
				if workload.fixture == "map" {
					values = append(values, hff.Object(nil))
				}
			}
			if workload.name == "AnyRootControl" {
				values = append(values, hff.String("wrong category"), hff.Int(1), hff.Object(nil))
			}
			table := fmt.Sprintf("guided_%d", len(expected))
			fmt.Fprintf(&script, "CREATE TEMP TABLE %s(id integer,value %s,metadata jsonb);\n", table, format)
			for i, value := range values {
				fmt.Fprintf(&script, "INSERT INTO %s VALUES (%d,%s::%s,%s::jsonb);\n", table, i,
					sqlFixtureString(metadataJSONFixture(t, value)), format, sqlFixtureString(pairTypeMetadata(t, value)))
			}
			for _, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(comparisonBinding(format)))
				if err != nil {
					t.Fatal(source, err)
				}
				var matches []bool
				for _, value := range values {
					yes, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": value}))
					if err != nil {
						t.Fatal(workload.name, source, err)
					}
					matches = append(matches, yes)
				}
				var types, args []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, args = append(types, typ), append(args, literal)
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
				}
				id := len(expected)
				fmt.Fprintf(&script, "PREPARE guided_query_%d%s AS SELECT json_agg(%s ORDER BY id) FROM %s records; EXECUTE guided_query_%d%s;\n", id, parameters, q.Where(), table, id, arguments)
				expected = append(expected, matches)
				descriptions = append(descriptions, string(format)+" "+workload.name+" "+source)
			}
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	count := 0
	for i, want := range expected {
		var got []bool
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(descriptions[i], err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v want %v", descriptions[i], got, want)
		}
		count += len(want)
	}
	t.Logf("checked %d schema-guided comparison results", count)
}
