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

func TestPostgreSQLNumericKernelReuseMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	integers := []hff.Value{hff.Null(), hff.Int(math.MinInt64), hff.Int(-9007199254740993), hff.Int(-1), hff.Int(0), hff.Int(1), hff.Int(9007199254740992), hff.Int(9007199254740993), hff.Int(math.MaxInt64)}
	floats := []hff.Value{hff.Null(), hff.Float(-math.MaxFloat64), hff.Float(-9223372036854775808), hff.Float(math.Nextafter(-9223372036854775808, math.Inf(-1))), hff.Float(-9007199254740992), hff.Float(-1.5), hff.Float(-1), hff.Float(-math.SmallestNonzeroFloat64), hff.Float(math.Copysign(0, -1)), hff.Float(0), hff.Float(math.SmallestNonzeroFloat64), hff.Float(0.5), hff.Float(1), hff.Float(1.5), hff.Float(9007199254740992), hff.Float(math.Nextafter(9007199254740992, math.Inf(1))), hff.Float(math.Nextafter(9223372036854775808, math.Inf(-1))), hff.Float(9223372036854775808), hff.Float(math.MaxFloat64)}
	sources := []string{
		`i<f`, `i<f i!=f f>i`, `i<f j<f`, `i=f | i!=f`, `f<i | f>=i`,
		`(i<f | j<f) !(i=f)`, `i!<f i<f!`, `i!<f | i<f`, `!(i!<f)`,
		`choice=choice`, `choice=other`, `choice<choice`, `choice<=choice`, `!(choice!=choice)`,
		`items:any(x=>x=x)`, `items:any(x=>x:[int]<f x:[int]!=f)`,
		`floats:any(x=>i<x j<x)`, `floats:any(f=>i<f j<f)`,
		`floats:any(x=>floats:any(x=>i<x j<x))`,
		`floats:map(x=>i<x j<x)=[true]`,
		`floats:foldl(false,(a,f)=>i<f j<f)`, `floats:foldr(false,(f,a)=>i<f j<f)`,
		`items:any(x=>floats:any(f=>x:[int]<f x:[int]!=f))`, `data=data`,
	}
	var records []hff.Value
	for ii, integer := range integers {
		for fi, floating := range floats {
			choice := integer
			if fi%3 == 0 {
				choice = floating
			} else if fi%3 == 1 {
				choice = hff.String("1.0")
			}
			items := hff.List(integer, floating, choice, hff.Null(), hff.String("x"))
			fs := hff.List(floating, hff.Float(2.5), hff.Null())
			if ii == 0 {
				items, fs = hff.List(), hff.List()
			} else if ii == 1 {
				items, fs = hff.Null(), hff.Null()
			}
			records = append(records, hff.Object(map[string]hff.Value{
				"i": integer, "j": integers[(ii+fi)%len(integers)], "f": floating,
				"choice": choice, "other": floating, "items": items, "floats": fs,
				"data": hff.Object(map[string]hff.Value{"nested": hff.List(integer, floating, choice)}),
			}))
		}
	}
	e := numericKernelEngine(t)
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		var rows []string
		for ri, record := range records {
			values, _ := record.AsObject()
			row := []string{sqlFixtureValue(t, values["i"], "bigint"), sqlFixtureValue(t, values["j"], "bigint"), sqlFixtureValue(t, values["f"], "double precision")}
			fields := []string{"choice", "other", "data", "items", "floats"}
			for _, field := range fields {
				literal := sqlFixtureString(metadataJSONFixture(t, values[field]))
				if values[field].Kind() == hff.NullKind && ri%2 == 0 {
					literal = "NULL"
				}
				row = append(row, literal+"::"+string(format))
			}
			for _, field := range fields {
				row = append(row, sqlFixtureString(pairTypeMetadata(t, values[field]))+"::jsonb")
			}
			rows = append(rows, "("+strings.Join(row, ",")+")")
		}
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			f, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, numericKernelOptions(format, format == hffsql.JSONB)...)
			if err != nil {
				t.Fatal(format, source, err)
			}
			var types, args []string
			for _, arg := range q.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types, args = append(types, typ), append(args, literal)
			}
			parameters, arguments := "", ""
			if len(types) != 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			for _, digits := range []int{1, 3} {
				index := len(expected)
				fmt.Fprintf(&script, "SET extra_float_digits=%d; PREPARE reused_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(i,j,f,choice,other,data,items,floats,choice_meta,other_meta,data_meta,items_meta,floats_meta); EXECUTE reused_%d%s;\n", digits, index, parameters, q.Where(), strings.Join(rows, ","), index, arguments)
				for ri, record := range records {
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(source, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s digits%d %s row%d", format, digits, source, ri))
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
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if result == "n" || (result == "true") != expected[i] || (result != "true" && result != "false") {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d scope-local numeric kernel results", len(expected))
}
