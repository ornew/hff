//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLJSONFloatUnderflow(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		source, input string
		want          bool
	}{
		{`data=0`, `1e-400`, true}, {`data`, `1e-999999999999999999999999`, false},
		{`!data`, `-1e-9999`, true}, {`data+1=1`, `1e-400`, true},
		{`data:[float]:string="-0.0"`, `-1e-400`, true},
		{`data:[float]:json="0.0"`, `0.000e+999999999999999999999999`, true},
		{`data:[float]=0`, `2.4703282292062327e-324`, true},
		{`data:[float]=5e-324`, `2.4703282292062328e-324`, true},
		{`data`, `[1e-999999]`, true}, {`data`, `{"x":1e-999999}`, true},
		{`data:[list]`, `[1e-999999]`, true}, {`data:[object]`, `{"x":1e-999999}`, true},
	}
	var script strings.Builder
	for i, tc := range cases {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSON)))
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
		fmt.Fprintf(&script, "PREPARE tiny_%d%s AS SELECT %s FROM (VALUES (%s::json)) records(data); EXECUTE tiny_%d%s;\n", i, parameters, q.Where(), sqlFixtureString(tc.input), i, arguments)
	}
	cmd := exec.Command("docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(cases) {
		t.Fatalf("got %d want %d: %s", len(results), len(cases), out)
	}
	for i, r := range results {
		if (r == "t") != cases[i].want {
			t.Errorf("%s input%s: SQL=%s want%v", cases[i].source, cases[i].input, r, cases[i].want)
		}
	}
}
