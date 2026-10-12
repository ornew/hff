//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
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

func TestPostgreSQLFloatOperandScopes(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("set HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Float("left")), schema.Nullable(schema.Float("right")), schema.List("items", schema.NullableType(schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{
		`left!+right!>0 left!-right!<100`,
		`left:[float]+right:[float]>0 left:[float]-right:[float]<100`,
		`items:any(x=>x+left>0 x-right<0)`,
		`items:any(x=>items:any(x=>x+left>0 x-right<0))`,
		`items:foldl(0.0,(acc,x)=>acc+x-left)>0`,
		`items:any(x=>x!+left>0 x!-right<0)`,
		`items:any(x=>items:any(y=>y+x>0 y-x<100))`,
	}
	options := append(arithmeticOptions(), hffsql.WithBindings(hffsql.Bind(hff.Path{"left"}, hffsql.Column("records", "left")), hffsql.Bind(hff.Path{"right"}, hffsql.Column("records", "right")), hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE records(id integer,\"left\" double precision,\"right\" double precision,items json);\n")
	var records []hff.Value
	for i := range 24 {
		l, r := arithmeticFixture(i)
		items := hff.List(l, r, hff.Float(1), hff.Null())
		records = append(records, hff.Object(map[string]hff.Value{"left": l, "right": r, "items": items}))
		fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s,%s,%s::json);\n", i, sqlFixtureValue(t, l, "double precision"), sqlFixtureValue(t, r, "double precision"), sqlFixtureString(metadataJSONFixture(t, items)))
	}
	var expected [][]bool
	for i, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		f, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, options...)
		if err != nil {
			t.Fatal(source, err)
		}
		var want []bool
		for _, record := range records {
			match, err := f.Match(ctx, record)
			if err != nil {
				t.Fatal(source, err)
			}
			want = append(want, match)
		}
		expected = append(expected, want)
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		params, values := "", ""
		if len(types) > 0 {
			params = "(" + strings.Join(types, ",") + ")"
			values = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE scoped_%d%s AS SELECT json_agg(%s ORDER BY id) FROM records; EXECUTE scoped_%d%s;\n", i, params, q.Where(), i, values)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for i, want := range expected {
		var got []bool
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: SQL%v local%v", sources[i], got, want)
		}
	}
}
