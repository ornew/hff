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
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestPostgreSQLTypedNumericResultPresenceAndEdges(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e := numericPruningEngine(t)
	var script strings.Builder
	var labels []string
	cases := []struct {
		source      string
		i, f, guard hff.Value
	}{
		// Numeric type failure is ordinary null, but a guarded RHS still fails
		// presence unless the original left operand was null and skipped it.
		{`(i+guard!)=null`, hff.String("x"), hff.Null(), hff.Int(2)},
		{`!(i+guard!):exists`, hff.String("x"), hff.Null(), hff.Null()},
		{`!((i+guard!)=null)`, hff.String("x"), hff.Null(), hff.Null()},
		{`(i+guard!)=null`, hff.Null(), hff.Null(), hff.Null()},
		{`!((f+guard!)=null)`, hff.Null(), hff.String("x"), hff.Null()},
		{`(f+guard!)=null`, hff.Null(), hff.Null(), hff.Null()},
		{`(i+1)=null`, hff.Int(math.MaxInt64), hff.Null(), hff.Int(2)},
		{`(-i)=null`, hff.Int(math.MinInt64), hff.Null(), hff.Int(2)},
		{`(i/0)=null`, hff.Int(1), hff.Null(), hff.Int(2)},
		{`(i/2)=2.5`, hff.Int(5), hff.Null(), hff.Int(2)},
		{`(f*2.0)=null`, hff.Null(), hff.Float(math.MaxFloat64), hff.Int(2)},
		{`(f/0.0)=null`, hff.Null(), hff.Float(1), hff.Int(2)},
		// These expectations follow PostgreSQL's native Float profile. Its
		// decimal fallback for the smallest subnormal differs from Go division.
		{`(f/2.0)=5e-324`, hff.Null(), hff.Float(math.SmallestNonzeroFloat64), hff.Int(2)},
		{`(f/2.0)=-5e-324`, hff.Null(), hff.Float(-math.SmallestNonzeroFloat64), hff.Int(2)},
		{`(f/1e100)=null`, hff.Null(), hff.Float(math.SmallestNonzeroFloat64), hff.Int(2)},
		{`(f+f)=0`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`(f*2.0):json="-0.0"`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`(f/2.0):json="-0.0"`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`(-f):json="0.0"`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`f:round:json="-0.0"`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`f:floor:json="-0.0"`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`f:ceil:json="-0.0"`, hff.Null(), hff.Float(math.Copysign(0, -1)), hff.Int(2)},
		{`(f+0.0)=f`, hff.Null(), hff.Float(math.Nextafter(1, 2)), hff.Int(2)},
		{`f:round=2.0`, hff.Null(), hff.Float(2.5), hff.Int(2)},
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, digits := range []int{1, 3} {
			for _, tc := range cases {
				p, err := e.Compile(ctx, tc.source)
				if err != nil {
					t.Fatal(tc.source, err)
				}
				q, err := hffsql.Build(ctx, p, numericPruningOptions(format, format == hffsql.JSONB)...)
				if err != nil {
					t.Fatal(tc.source, err)
				}
				var types, args []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					args = append(args, literal)
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				// Exercise both SQL-null and JSON-null storage for nullable operands.
				for _, sqlNull := range []bool{false, true} {
					row := []string{}
					for _, v := range []hff.Value{tc.i, tc.f} {
						literal := sqlFixtureString(metadataJSONFixture(t, v))
						if sqlNull && v.Kind() == hff.NullKind {
							literal = "NULL"
						}
						row = append(row, literal+"::"+string(format))
					}
					row = append(row, sqlFixtureString(pairTypeMetadata(t, tc.i))+"::jsonb", sqlFixtureString(pairTypeMetadata(t, tc.f))+"::jsonb")
					guard := "NULL::bigint"
					if v, ok := tc.guard.AsInt(); ok {
						guard = fmt.Sprintf("'%d'::bigint", v)
					}
					row = append(row, guard)
					index := len(labels)
					fmt.Fprintf(&script, "SET extra_float_digits=%d; PREPARE typed_%d%s AS SELECT %s FROM (VALUES (%s)) records(i,f,i_meta,f_meta,guard); EXECUTE typed_%d%s;\n", digits, index, parameters, q.Where(), strings.Join(row, ","), index, arguments)
					labels = append(labels, fmt.Sprintf("%s digits%d sqlNull%v %s", format, digits, sqlNull, tc.source))
				}
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
	if len(results) != len(labels) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(labels), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s returned %s", labels[i], result)
		}
	}
	t.Logf("checked %d typed-result edge expectations", len(labels))
}
