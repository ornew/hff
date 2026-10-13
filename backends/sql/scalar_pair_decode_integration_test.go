//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
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

// Stored schemas determine subtype identity even when physical JSON spelling
// changes: fixed Int accepts 1.0, and fixed Float accepts integral spellings
// above int64. JSONB can remove exponents and cannot be classified lexically.
func TestPostgreSQLScalarPairPhysicalSpellings(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type row struct {
		left, right string
		values      [2]hff.Value
	}
	for _, profile := range []struct {
		name        string
		left, right hff.Type
		rows        []row
	}{
		{"float_float", schema.FloatType, schema.FloatType, []row{
			{"100000000000000000000", "1e20", [2]hff.Value{hff.Float(1e20), hff.Float(1e20)}},
			{"1.00000000000000001", "1", [2]hff.Value{hff.Float(1), hff.Float(1)}},
			{"-2e-324", "0", [2]hff.Value{hff.Float(math.Copysign(0, -1)), hff.Float(0)}},
			{"3e-324", "5e-324", [2]hff.Value{hff.Float(math.SmallestNonzeroFloat64), hff.Float(math.SmallestNonzeroFloat64)}},
		}},
		{"float_int", schema.FloatType, schema.IntType, []row{
			{"9223372036854775808", "9223372036854775807", [2]hff.Value{hff.Float(9223372036854775808), hff.Int(math.MaxInt64)}},
			{"9007199254740992", "9007199254740993.0", [2]hff.Value{hff.Float(9007199254740992), hff.Int(9007199254740993)}},
			{"-2e-324", "0.0", [2]hff.Value{hff.Float(math.Copysign(0, -1)), hff.Int(0)}},
			{"-9223372036854775808", "-9223372036854775808.0", [2]hff.Value{hff.Float(-9223372036854775808), hff.Int(math.MinInt64)}},
		}},
		{"int_float", schema.IntType, schema.FloatType, []row{
			{"9223372036854775807.0", "9223372036854775808", [2]hff.Value{hff.Int(math.MaxInt64), hff.Float(9223372036854775808)}},
			{"9007199254740993.0", "9007199254740992", [2]hff.Value{hff.Int(9007199254740993), hff.Float(9007199254740992)}},
			{"0.0", "-2e-324", [2]hff.Value{hff.Int(0), hff.Float(math.Copysign(0, -1))}},
		}},
		{"int_int", schema.IntType, schema.IntType, []row{
			{"9223372036854775807.0", "9223372036854775807", [2]hff.Value{hff.Int(math.MaxInt64), hff.Int(math.MaxInt64)}},
			{"1.0", "1e0", [2]hff.Value{hff.Int(1), hff.Int(1)}},
		}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			left := schema.Union(schema.StringType, profile.left)
			right := schema.Union(schema.StringType, profile.right)
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", left), schema.Field("right", right))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var script strings.Builder
				var expected []bool
				var labels []string
				for si, source := range []string{`left=right`, `left!=right`, `left<right`, `right<left`, `left>=right`, `(left<right):type="null"`, `left:[float]=right`, `left=right:[float]`} {
					p, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, p)
					if err != nil {
						t.Fatal(err)
					}
					q := pairDispatchQuery(t, left, right, source, format, false)
					var types, args, rows []string
					for _, arg := range q.Args() {
						typ, literal := sqlFixtureParameter(t, arg)
						types, args = append(types, typ), append(args, literal)
					}
					for _, row := range profile.rows {
						want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": row.values[0], "right": row.values[1]}))
						if err != nil {
							t.Fatal(err)
						}
						rows = append(rows, "("+sqlFixtureString(row.left)+"::"+string(format)+","+sqlFixtureString(row.right)+"::"+string(format)+")")
						expected = append(expected, want)
						labels = append(labels, source+" "+row.left+" / "+row.right)
					}
					parameters, arguments := "", ""
					if len(types) > 0 {
						parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
					}
					fmt.Fprintf(&script, "PREPARE raw_%d%s AS SELECT %s FROM (VALUES %s) records(\"left\",\"right\"); EXECUTE raw_%d%s;\n", si, parameters, q.Where(), strings.Join(rows, ","), si, arguments)
				}
				cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				cmd.Stdin = strings.NewReader(script.String())
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v\n%s", format, err, output)
				}
				results := strings.Fields(string(output))
				if len(results) != len(expected) {
					t.Fatalf("got %d rows,want %d: %s", len(results), len(expected), output)
				}
				for i, result := range results {
					if (result == "t") != expected[i] {
						t.Errorf("%s %s: SQL=%s local=%v", format, labels[i], result, expected[i])
					}
				}
				t.Logf("%s: checked %d physical-spelling pairs", format, len(expected))
			}
		})
	}
}
