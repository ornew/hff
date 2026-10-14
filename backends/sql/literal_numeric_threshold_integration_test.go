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
)

func TestPostgreSQLExactLiteralThresholdsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	integers := []hff.Value{hff.Null(), hff.Int(math.MinInt64), hff.Int(math.MinInt64 + 1), hff.Int(-9007199254740993), hff.Int(-19), hff.Int(-1), hff.Int(0), hff.Int(1), hff.Int(18), hff.Int(19), hff.Int(9007199254740992), hff.Int(9007199254740993), hff.Int(math.MaxInt64 - 1), hff.Int(math.MaxInt64)}
	floats := []hff.Value{hff.Null(), hff.Float(-math.MaxFloat64), hff.Float(math.Nextafter(-9223372036854775808, math.Inf(-1))), hff.Float(-9223372036854775808), hff.Float(math.Nextafter(-9223372036854775808, 0)), hff.Float(-9007199254740992), hff.Float(-18.5), hff.Float(-1.5), hff.Float(-math.SmallestNonzeroFloat64), hff.Float(math.Copysign(0, -1)), hff.Float(0), hff.Float(math.SmallestNonzeroFloat64), hff.Float(0.5), hff.Float(1), hff.Float(18.5), hff.Float(math.Nextafter(18.5, 0)), hff.Float(math.Nextafter(18.5, math.Inf(1))), hff.Float(9007199254740992), hff.Float(9007199254740994), hff.Float(math.Nextafter(9223372036854775808, 0)), hff.Float(9223372036854775808), hff.Float(math.MaxFloat64)}
	floatLiterals := []string{"-1.7976931348623157e308", "-9223372036854775808.0", "-9007199254740992.0", "-18.5", "-5e-324", "-0.0", "0.0", "5e-324", "0.5", "18.5", "9007199254740992.0", "9007199254740994.0", "9223372036854775808.0", "1.7976931348623157e308"}
	intLiterals := []string{"-9223372036854775808", "-9007199254740993", "-19", "-1", "0", "1", "18", "9007199254740993", "9223372036854775807"}
	operators := []string{"=", "!=", "<", "<=", ">", ">="}
	type fixture struct {
		source  string
		records []hff.Value
	}
	var fixtures []fixture
	for _, side := range []struct {
		field    string
		values   []hff.Value
		literals []string
	}{
		{"i", integers, floatLiterals}, {"f", floats, intLiterals},
	} {
		var records []hff.Value
		for vi, value := range side.values {
			i, f := integers[vi%len(integers)], floats[vi%len(floats)]
			if side.field == "i" {
				i = value
			} else {
				f = value
			}
			records = append(records, hff.Object(map[string]hff.Value{"i": i, "f": f, "name": hff.String("tail")}))
		}
		for _, literal := range side.literals {
			for _, operator := range operators {
				base := side.field + operator + literal
				for _, source := range []string{base, "!(" + base + ")", side.field + "!" + operator + literal} {
					fixtures = append(fixtures, fixture{source, records})
				}
			}
		}
	}
	var records []hff.Value
	for _, i := range integers {
		for _, f := range floats {
			records = append(records, hff.Object(map[string]hff.Value{"i": i, "f": f, "name": hff.String("tail")}))
		}
	}
	for _, source := range []string{
		`i>18.5 name="tail" f>"9007199254740993":number`,
		`i>18.5 | f<=9007199254740993`, `!(i>18.5 | f<=9007199254740993)`,
		`i=0.5 | i!=0.5`, `!(i>=9223372036854775808.0)`,
		`(i+0)>18.5`, `(i/1)>9007199254740993`, `!((i/1)=9007199254740993)`,
		`(i>18.5):type="bool"`, `[18.5<i][0]`,
		`i>"18.5":number`, `i>"bad":number`, `f>"bad":number`, `i>1e999`,
	} {
		fixtures = append(fixtures, fixture{source, records})
	}
	e := literalThresholdEngine(t)
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, fixture := range fixtures {
		p, err := e.Compile(ctx, fixture.source)
		if err != nil {
			t.Fatal(fixture.source, err)
		}
		f, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, literalThresholdOptions()...)
		if err != nil {
			t.Fatal(fixture.source, err)
		}
		var rows []string
		for _, record := range fixture.records {
			values, _ := record.AsObject()
			rows = append(rows, "("+sqlFixtureValue(t, values["i"], "bigint")+","+sqlFixtureValue(t, values["f"], "double precision")+",'tail'::text)")
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		for _, digits := range []int{1, 3} {
			index := len(expected)
			fmt.Fprintf(&script, "SET extra_float_digits=%d; PREPARE literal_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(i,f,name); EXECUTE literal_%d%s;\n", digits, index, parameters, q.Where(), strings.Join(rows, ","), index, arguments)
			for ri, record := range fixture.records {
				want, err := f.Match(ctx, record)
				if err != nil {
					t.Fatal(fixture.source, err)
				}
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("digits%d %s row%d", digits, fixture.source, ri))
			}
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if result == "n" || (result == "true") != expected[i] || (result != "true" && result != "false") {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d exact literal threshold results", len(expected))
}
