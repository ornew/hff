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

func TestPostgreSQLMixedPredicatesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, bindings := mixedPredicateEngine(t)
	type recordCase struct{ age, name, factor, raw, payload, tags hff.Value }
	cases := []recordCase{
		{hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null()},
		{hff.Int(0), hff.String(""), hff.Float(0), hff.String(""), hff.Object(nil), hff.List()},
		{hff.Int(19), hff.String("Alice"), hff.Float(1.5), hff.String(`{"price":20,"active":true}`), hff.Object(map[string]hff.Value{"left": hff.String("a"), "right": hff.String("b"), "f": hff.Float(1)}), hff.List(hff.String("auth"), hff.String("dev"))},
		{hff.Int(5), hff.String("Bob"), hff.Float(0.5), hff.String(`{"price":0,"active":false}`), hff.Object(map[string]hff.Value{"left": hff.String("b"), "right": hff.Null()}), hff.List(hff.String("bob"))},
		{hff.Int(25), hff.String("Ann"), hff.Float(math.MaxFloat64), hff.String(`{bad`), hff.Object(map[string]hff.Value{"left": hff.Null(), "right": hff.String("b")}), hff.List(hff.String("auth"))},
		{hff.Int(1), hff.String("auth"), hff.Float(math.Copysign(0, -1)), hff.String(`null`), hff.Null(), hff.List(hff.String("auth"))},
		{hff.Int(19), hff.Null(), hff.Null(), hff.String(`{}`), hff.Object(nil), hff.List()},
		{hff.Null(), hff.String("Alice"), hff.Float(2.5), hff.String(`{"price":"bad","active":0}`), hff.Object(map[string]hff.Value{"left": hff.Int(1), "right": hff.String("a")}), hff.List(hff.String("a"))},
	}
	sources := []string{
		`age>18 name~"^A"`, `!(age>18|name~"A")`, `age>18 name!~"^A"`,
		`age>18 !(name~"(")`, `age+1>10 name="Alice" age+2<30`,
		`age>18 raw!:fromjson.price>10`, `!(age>18 raw!:fromjson.price>10)`,
		`raw:fromjson.price>10 age>18 raw:fromjson.active`,
		`(age>18 raw:fromjson.price>10)|(age<5 raw:fromjson.active)`,
		`(age>18 name~"A")|(age<5 raw:fromjson.active)`,
		`(age>18 ` + "`raw.name`" + `:fromjson.price>10)|(age<5 raw:fromjson.active)`,
		`payload:size>0 age>18 payload.left="a"`,
		`(age>18 payload.left="a")|(age<5 payload.right="b")`,
		`age>18 tags:any(age=>age:contains("auth"))`,
		`age>18 tags:any(tag=>name:contains(tag))`,
		`name="Alice" tags:any(tag=>raw:fromjson.active)`,
		`age>18 !(factor+1.0>2.0|raw:fromjson.active)`,
		`age>18 name:matches("^A") raw:fromjson.active`,
		`(name~"A" raw:fromjson.price>10)|(name!~"B" raw:fromjson.active)`,
		`name!~"[" | raw:fromjson.active`,
		`tags:any(tag=>name:matches("^A"))`,
		`tags:any(tag=>!name!:matches("^A"))`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for si, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, literal)
		}
		var rows []string
		for ri, record := range cases {
			rawName := hff.String(`{"price":42,"active":false}`)
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
				"age": record.age, "name": record.name, "factor": record.factor, "raw": record.raw,
				"raw.name": rawName, "payload": record.payload, "tags": record.tags, "created": hff.Null(),
			}))
			if err != nil {
				t.Fatal(source, err)
			}
			values := []string{
				sqlFixtureValue(t, record.age, "bigint"), sqlFixtureValue(t, record.name, "text"),
				sqlFixtureValue(t, record.factor, "double precision"), sqlFixtureValue(t, record.raw, "text"),
				sqlFixtureValue(t, rawName, "text"),
				sqlFixtureString(metadataJSONFixture(t, record.payload)) + "::jsonb",
				sqlFixtureString(pairTypeMetadata(t, record.payload)) + "::jsonb",
				sqlFixtureString(metadataJSONFixture(t, record.tags)) + "::jsonb", "NULL::timestamptz",
			}
			rows = append(rows, "("+strings.Join(values, ",")+")")
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s record %d", source, ri))
		}
		parameters, arguments := "", ""
		if len(types) != 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE mixed_predicate_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) AS records(age,name,factor,raw,raw_name,payload,payload_meta,tags,created); EXECUTE mixed_predicate_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
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
		if result == "n" || (result == "true") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}
