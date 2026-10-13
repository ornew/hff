//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
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

func TestPostgreSQLFloatArithmetic(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Float("left"), schema.Float("right"), schema.Float("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	negativeZero := math.Copysign(0, -1)
	minimum := math.SmallestNonzeroFloat64
	pairs := [][2]float64{
		{0, 0}, {negativeZero, 0}, {0, negativeZero}, {negativeZero, negativeZero},
		{0.1, 0.2}, {1, 3}, {-1, 3}, {5, 2},
		{minimum, 0.5}, {minimum, 1.5}, {3 * minimum, 0.5}, {5 * minimum, 0.5},
		{minimum, 2}, {3 * minimum, 2}, {5 * minimum, 2}, {-3 * minimum, 2},
		{math.Ldexp(1, -1022), 2}, {math.Nextafter(math.Ldexp(1, -1021), 0), 2},
		{math.MaxFloat64, 2}, {math.MaxFloat64, math.MaxFloat64},
		{math.MaxFloat64, math.Ldexp(1, 970)}, {math.MaxFloat64, math.Nextafter(math.Ldexp(1, 970), 0)},
		{1e-308, 1e308}, {-1e-308, 1e308}, {1e308, 1e-308},
		{9007199254740992, 1}, {9007199254740992, 3},
	}
	random := rand.New(rand.NewPCG(0x754, 0x1000))
	for len(pairs) < 75 {
		a, b := math.Float64frombits(random.Uint64()), math.Float64frombits(random.Uint64())
		if !math.IsNaN(a) && !math.IsInf(a, 0) && !math.IsNaN(b) && !math.IsInf(b, 0) {
			pairs = append(pairs, [2]float64{a, b})
		}
	}
	var script strings.Builder
	var labels []string
	for index, op := range []string{"+", "-", "*", "/", "+", "-", "*", "/"} {
		operand := "right"
		if index >= 4 {
			operand = "left"
		}
		source := "(left" + op + operand + ")=expected"
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.Bind(hff.Path{"left"}, hffsql.Column("records", "left")),
			hffsql.Bind(hff.Path{"right"}, hffsql.Column("records", "right")),
			hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected")),
		))
		if err != nil {
			t.Fatal(err)
		}
		var rows []string
		for _, pair := range pairs {
			if operand == "left" {
				pair[1] = pair[0]
			}
			// Compare values, not portable decimal spellings. Native ordinary
			// arithmetic and cancellation retain the host's binary64 result;
			// extreme fallback rounding is covered by separate backend cases.
			absLeft, absRight := math.Abs(pair[0]), math.Abs(pair[1])
			safe := false
			switch op {
			case "+":
				safe = (absLeft <= 1e300 && absRight <= 1e300) || (pair[0] >= 0 && pair[1] <= 0) || (pair[0] <= 0 && pair[1] >= 0)
			case "-":
				safe = (absLeft <= 1e300 && absRight <= 1e300) || (pair[0] >= 0 && pair[1] >= 0) || (pair[0] <= 0 && pair[1] <= 0)
			case "*", "/":
				safe = absLeft >= 1e-100 && absLeft <= 1e100 && absRight >= 1e-100 && absRight <= 1e100
				if pair[0] == 0 || (op == "*" && pair[1] == 0) {
					safe = true
				}
				if op == "/" && pair[1] == 0 {
					safe = false
				}
			}
			if !safe {
				continue
			}
			var value float64
			switch op {
			case "+":
				value = pair[0] + pair[1]
			case "-":
				value = pair[0] - pair[1]
			case "*":
				value = pair[0] * pair[1]
			case "/":
				value = pair[0] / pair[1]
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatal("non-finite value in native-safe fixture")
			}
			rows = append(rows, "("+sqlFixtureValue(t, hff.Float(pair[0]), "double precision")+","+sqlFixtureValue(t, hff.Float(pair[1]), "double precision")+","+sqlFixtureValue(t, hff.Float(value), "double precision")+")")
			labels = append(labels, fmt.Sprintf("%g %s %g -> %g", pair[0], op, pair[1], value))
		}
		fmt.Fprintf(&script, "PREPARE floats_%d AS SELECT %s FROM (VALUES %s) records(\"left\",\"right\",expected); EXECUTE floats_%d; DEALLOCATE floats_%d;\n", index, query.Where(), strings.Join(rows, ","), index, index)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(labels) {
		t.Fatalf("got%d results want%d: %s", len(results), len(labels), output)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s: SQL=%s", labels[i], result)
		}
	}
}
