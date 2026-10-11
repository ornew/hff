//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestPostgreSQLJSONParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	obj := func(v map[string]hff.Value) hff.Value { return hff.Object(v) }
	type row struct {
		values    []hff.Value
		documents []string
		sqlNull   bool
	}
	rows := []row{
		{[]hff.Value{hff.Null(), hff.Null(), hff.Null(), hff.Null()}, []string{"null", "null", "null", "null"}, true},
		{[]hff.Value{hff.Null(), hff.Null(), hff.Null(), hff.Null()}, []string{"null", "null", "null", "null"}, false},
		{[]hff.Value{obj(map[string]hff.Value{"name": hff.String("Alice"), "age": hff.Int(18)}), hff.List(hff.Int(10), hff.Int(20), hff.Null()), obj(map[string]hff.Value{"app": hff.String("auth"), "team": hff.Null()}), hff.List(hff.Float(1))}, []string{`{"name":"Alice","age":18}`, `[10,20,null]`, `{"app":"auth","team":null}`, `[1.0]`}, false},
		{[]hff.Value{obj(map[string]hff.Value{}), hff.List(), obj(map[string]hff.Value{}), hff.List()}, []string{`{}`, `[]`, `{}`, `[]`}, false},
		{[]hff.Value{obj(map[string]hff.Value{"name": hff.Null(), "age": hff.Int(-1)}), hff.List(hff.Null()), obj(map[string]hff.Value{"app": hff.String("other")}), hff.List(hff.Float(-0.0))}, []string{`{"name":null,"age":-1}`, `[null]`, `{"app":"other"}`, `[-0.0]`}, false},
	}
	for _, n := range []int64{9007199254740991, 9007199254740992, 9007199254740993, math.MaxInt64, math.MinInt64} {
		rows = append(rows, row{[]hff.Value{obj(map[string]hff.Value{"name": hff.String("Alice"), "age": hff.Int(n), "father": obj(map[string]hff.Value{"name": hff.String("Bob")})}), hff.List(), obj(map[string]hff.Value{}), hff.List(hff.Float(math.SmallestNonzeroFloat64), hff.Float(math.MaxFloat64))}, []string{`{"name":"Alice","age":` + strconv.FormatInt(n, 10) + `,"father":{"name":"Bob"}}`, `[]`, `{}`, `[5e-324,1.7976931348623157e308]`}, false})
	}
	for i := range rows {
		value := hff.List(hff.List(hff.Int(10), hff.Int(20)), hff.List(), hff.Null(), hff.List(hff.Null()))
		document := `[[10,20],[],null,[null]]`
		if i < 2 {
			value = hff.Null()
			document = "null"
		} else if i == 3 {
			value = hff.List()
			document = "[]"
		}
		rows[i].values = append(rows[i].values, value)
		rows[i].documents = append(rows[i].documents, document)
		people := hff.List(rows[i].values[0], hff.Null())
		peopleJSON := "[" + rows[i].documents[0] + ",null]"
		if i < 2 {
			people = hff.Null()
			peopleJSON = "null"
		}
		rows[i].values = append(rows[i].values, people)
		rows[i].documents = append(rows[i].documents, peopleJSON)
	}
	e := jsonEngine(t)
	var script strings.Builder
	var expected []string
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		table := "hff_fixture_" + string(format)
		fmt.Fprintf(&script, "CREATE TEMP TABLE %s (id integer,profile %s,scores %s,labels %s,factors %s,groups %s,people %s);\n", table, format, format, format, format, format, format)
		for ri, r := range rows {
			var data []string
			for _, document := range r.documents {
				v := sqlFixtureString(document) + "::" + string(format)
				if r.sqlNull {
					v = "NULL::" + string(format)
				}
				data = append(data, v)
			}
			fmt.Fprintf(&script, "INSERT INTO %s VALUES (%d,%s);\n", table, ri, strings.Join(data, ","))
		}
		for _, source := range jsonQueries() {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(format)...))
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for ri, r := range rows {
				record := obj(map[string]hff.Value{"profile": r.values[0], "scores": r.values[1], "labels": r.values[2], "factors": r.values[3], "groups": r.values[4], "people": r.values[5]})
				match, err := filter.Match(ctx, record)
				if err != nil {
					t.Fatal(source, ri, err)
				}
				want := "f"
				if match {
					want = "t"
				}
				expected = append(expected, want)
				descriptions = append(descriptions, fmt.Sprintf("%s %s row%d", format, source, ri))
				name := fmt.Sprintf("json_%d", len(expected))
				var types, literals []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					literals = append(literals, literal)
				}
				params, execution := "", ""
				if len(types) > 0 {
					params = "(" + strings.Join(types, ",") + ")"
					execution = "(" + strings.Join(literals, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM %s AS records WHERE records.id=%d;\nEXECUTE %s%s;\nDEALLOCATE %s;\n", name, params, query.Where(), table, ri, name, execution, name)
			}
		}
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("JSON conformance failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results; want%d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("%s PostgreSQL=%s local=%s", descriptions[i], actual[i], want)
		}
	}
	t.Logf("verified %d JSON query/record pairs", len(expected))
}
