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

func TestPostgreSQLNumericBranchPruningMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e := numericPruningEngine(t)
	pairs := [][4]hff.Value{
		{hff.Int(math.MinInt64), hff.Float(-3.125), hff.Int(math.MinInt64), hff.Int(math.MinInt64)},
		{hff.Int(math.MaxInt64), hff.Float(3.125), hff.Float(3.125), hff.Float(3.125)},
		{hff.Int(9007199254740993), hff.Float(0.75), hff.Int(9007199254740993), hff.Bool(false)},
		{hff.Int(5), hff.Float(1.25), hff.Float(1.25), hff.String("x")},
		{hff.Int(-5), hff.Float(-1.25), hff.String("x"), hff.Object(nil)},
		{hff.Int(0), hff.Float(math.Copysign(0, -1)), hff.Null(), hff.Null()},
		{hff.String("x"), hff.String("x"), hff.Float(0), hff.Int(5)},
		{hff.Null(), hff.Null(), hff.String("x"), hff.List()},
	}
	var records []hff.Value
	for pi, pair := range pairs {
		guard := hff.Int(2)
		if pi%3 == 0 {
			guard = hff.Null()
		} else if pi%3 == 1 {
			guard = hff.Int(0)
		}
		records = append(records, hff.Object(map[string]hff.Value{"i": pair[0], "f": pair[1], "both": pair[2], "data": pair[3], "items": hff.List(pair[0], hff.Int(5), hff.String("x"), hff.Null()), "floats": hff.List(pair[1], hff.Float(1.25), hff.String("x"), hff.Null()), "guard": guard, "name": hff.String("x")}))
	}
	var sources []string
	for _, op := range []string{"+", "-", "*", "/", "%"} {
		expressions := []string{"i" + op + "2", "f" + op + "2.0", "both" + op + "2", "data" + op + "2", "i" + op + "f", "data:[string|int]" + op + "2", "both:[string|float]" + op + "f"}
		if op == "%" {
			// Remainder statically requires Int candidates on both sides.
			expressions = []string{"i%2", "both%2", "data%2", "data:[string|int]%2"}
		}
		for _, expression := range expressions {
			for _, suffix := range []string{`>0`, `=null`, `:type="int"`, `:type="float"`, `:exists`} {
				sources = append(sources, "("+expression+")"+suffix)
			}
		}
	}
	for _, receiver := range []string{"i", "f", "both", "data", "data:[string|int]", "both:[string|float]"} {
		for _, suffix := range []string{`>0`, `=null`, `:type="int"`, `:type="float"`} {
			sources = append(sources, "(-"+receiver+")"+suffix)
		}
		for _, method := range []string{"round", "ceil", "floor"} {
			sources = append(sources, receiver+":"+method+`:type="int"`, receiver+":"+method+`:type="float"`, receiver+":"+method+">0")
		}
	}
	sources = append(sources, `i+guard!`, `!(i+guard!)`, `(i+guard!):exists`, `(i!+guard):type="null"`,
		`(i+2)*3>0`, `(f+2.0)*3.0>0`, `(i/2)+f>0`, `i:round+2>0`,
		`items:any(x=>x+2>0)`, `items:map(x=>x+2):contains(7)`, `items:foldl(0,(acc,x)=>acc+x)>0`,
		`floats:any(x=>x+f>0)`, `floats:any(x=>floats:any(x=>x+f>0))`,
		`items:any(x=>floats:any(x=>x+f>0) x-2<0)`,
		`(-f):json="0.0"`, `(-f):json="-0.0"`,
	)
	var script strings.Builder
	var expected []bool
	var labels []string
	fields := []string{"i", "f", "both", "data", "items", "floats"}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		var rows []string
		columns := append([]string(nil), fields...)
		for _, field := range fields {
			columns = append(columns, field+"_meta")
		}
		columns = append(columns, "guard", "name")
		for i, column := range columns {
			columns[i] = `"` + column + `"`
		}
		for ri, record := range records {
			values, _ := record.AsObject()
			var row []string
			for _, field := range fields {
				code := sqlFixtureString(metadataJSONFixture(t, values[field]))
				if values[field].Kind() == hff.NullKind && ri%2 == 0 {
					code = "NULL"
				}
				row = append(row, code+"::"+string(format))
			}
			for _, field := range fields {
				row = append(row, sqlFixtureString(pairTypeMetadata(t, values[field]))+"::jsonb")
			}
			guard := "NULL::bigint"
			if v, ok := values["guard"].AsInt(); ok {
				guard = fmt.Sprintf("'%d'::bigint", v)
			}
			row = append(row, guard, "'x'::text")
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
			q, err := hffsql.Build(ctx, p, numericPruningOptions(format, format == hffsql.JSONB)...)
			if err != nil {
				t.Fatal(source, err)
			}
			var types, args []string
			for _, arg := range q.Args() {
				typ, value := sqlFixtureParameter(t, arg)
				types, args = append(types, typ), append(args, value)
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			si := len(expected)
			fmt.Fprintf(&script, "PREPARE pruned_%d%s AS SELECT %s FROM (VALUES %s) records(%s); EXECUTE pruned_%d%s;\n", si, parameters, q.Where(), strings.Join(rows, ","), strings.Join(columns, ","), si, arguments)
			for ri, record := range records {
				want, err := f.Match(ctx, record)
				if err != nil {
					t.Fatal(source, ri, err)
				}
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s %s record%d", format, source, ri))
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
		t.Fatalf("got%d results,want%d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d numeric-family comparisons", len(expected))
}

func TestPostgreSQLNumericBranchPruningRetainsExtensionCarrier(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, d := foldedNullExtension(t)
	base := numericPruningEngine(t)
	e, err := engine.New(engine.WithSchema(base.Schema()), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ expression, carrier string }{
		{`i+2`, "bigint"}, {`i%2`, "bigint"}, {`-i`, "bigint"}, {`i:round`, "bigint"},
		{`i/2`, "double precision"}, {`f+2.0`, "double precision"}, {`-f`, "double precision"}, {`f:round`, "double precision"}, {`i+f`, "double precision"},
	}
	var script strings.Builder
	for index, tc := range cases {
		p, err := e.Compile(ctx, `name:nullcarrier(`+tc.expression+`)`)
		if err != nil {
			t.Fatal(err)
		}
		opts := numericPruningOptions(hffsql.JSON, false)
		opts = append(opts, hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "pg_typeof({})::text='"+tc.carrier+"'", args[0])
		})))
		q, err := hffsql.Build(ctx, p, opts...)
		if err != nil {
			t.Fatal(tc.expression, err)
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, value)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE carrier_%d%s AS SELECT %s FROM (VALUES (NULL::json,NULL::json,'x'::text), ('null'::json,'null'::json,'x'::text), ('\"x\"'::json,'\"x\"'::json,'x'::text), ('5'::json,'1.25'::json,'x'::text)) records(i,f,name); EXECUTE carrier_%d%s;\n", index, parameters, q.Where(), index, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != 4*len(cases) {
		t.Fatalf("got %d results, want %d: %s", len(results), 4*len(cases), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s lost %s carrier for row %d: %s", cases[i/4].expression, cases[i/4].carrier, i%4, result)
		}
	}
	t.Logf("checked %d extension carrier observations", len(results))
}
