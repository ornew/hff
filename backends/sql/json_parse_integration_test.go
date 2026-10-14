//go:build integration

package sql_test

import (
	"context"
	"fmt"
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

func TestPostgreSQLStrictJSONParsing(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stringsIn := []string{`null`, `true`, `false`, `1`, `1.0`, `-0`, `-0.0`, `1e0`, `-1e-9999999999999999999`, `1e999999`, `9223372036854775807`, `9223372036854775808`, `-9223372036854775809`, `1e-400`, `2.4703282292062327e-324`, `2.4703282292062328e-324`, `"a"`, `"1e-foo"`, `"e-"`, `{"text":"1e-foo"}`, `["1e-foo",true,null]`, `"\ud800\udc00"`, `"\ud800"`, `"\udc00"`, `"\ud800a"`, `"\x41"`, `"bad`, `[1,1.0]`, `[1,9223372036854775808]`, `{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"x":{"a":1,"a":2}}`, `{"a":1}`, `{"a":1e999999}`, `{"a":-1e-999999}`, `{"a":[1,1.0,null]}`, `[]`, `{}`, `1 2`, `01`, `NaN`, `Infinity`, ``, " \n true \t", `[1,]`, `{"a":}`, `{"a":null}`, `["1","bad","null"]`, `["{\"x\":1}","{\"x\":9223372036854775808}"]`, `{"a":1,"bad":{"n":9223372036854775808}}`}
	values := []hff.Value{hff.Null(), hff.Int(1), hff.Bool(true), hff.List(), hff.Object(nil)}
	stringsIn = append(stringsIn,
		`{"a":{"b":1}}`, `{"a":{"b":null}}`, `{"a":{"b":1.0}}`,
		`["{\"a\":1}","{\"a\":2}"]`, `{"a":9223372036854775807}`,
		`{"a":-0.0}`, `{"a":5e-324}`, `{"a":1,"invalid":1e400}`,
	)
	for _, s := range stringsIn {
		values = append(values, hff.String(s))
	}
	sources := []string{`data:fromjson=null`, `data:fromjson:type="number"`, `data:fromjson:[int]=1`, `data:fromjson:[float]=1`, `data:fromjson:json="null"`, `data:fromjson:json=data`, `data:fromjson:[float]:string="-0.0"`, `data!:fromjson:exists`, `data:fromjson:[object]:has("a")`, `data:fromjson:[list]:map(x=>x):json="[1,1.0]"`, `data:fromjson:[object].a`, `!data:fromjson`, `data:fromjson=("bad":fromjson)`, `data:fromjson=("1e999999":fromjson)`, `data:fromjson=(null:fromjson).a`, `data:fromjson=(null:fromjson)[0]`, `data:fromjson=null.a`, `data:fromjson=null[0]`, `data:fromjson=(null!).a`, `data:fromjson:type=(null:fromjson).a:type`, `data:fromjson:type=(null!).a:type`, `({"a":data}).a=data`, `[{"a":data}][0].a=data`, `data:fromjson:exists | data:fromjson=null`, `data:fromjson!:exists | data:fromjson=null`, `data!:fromjson:exists | data:fromjson=null`, `data:fromjson:type=data:fromjson:type`, `data:fromjson:type=data!:fromjson:type`, `data:fromjson:[list[string]]:any(data=>data:fromjson:type=data:fromjson:type)`}
	sources = append(sources,
		`data:fromjson.a=1 data:fromjson.a<2`,
		`data:fromjson.a=1 | data:fromjson.a=2`,
		`data:fromjson.a:type=data:fromjson.a:type`,
		`data:fromjson.a:json=data:fromjson.a:json`,
		`data:fromjson.a.b=1 data:fromjson.a.b<2`,
		`data!:fromjson.a=1 | data:fromjson.a=null`,
		`data:fromjson!.a=1 | data:fromjson.a=null`,
		`data:fromjson.a:[int]=1 data:fromjson.a:[float]=1.0`,
		`data:fromjson.a=(null!).a | data:fromjson.a:exists`,
		`data:fromjson:[list[string]]:any(data=>data:fromjson.a=1 data:fromjson.a<2)`,
		`data:fromjson:[list[string]]:all(x=>x:fromjson.a=1 | x:fromjson.a=2)`,
		`data:fromjson:[list[string]]:any(x=>x:fromjson.a=1 data:fromjson:[list[string]]:any(x=>x:fromjson.a=2))`,
		`data:fromjson:[list[string]]:any(x=>x:fromjson.a=1 x:fromjson.a:exists) data:fromjson.a=null`,
	)
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB, ""} {
		typ := schema.AnyType
		if format == "" {
			typ = schema.Union(schema.StringType, schema.NullType)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for si, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			f, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if format == hffsql.JSONB && (source == `({"a":data}).a=data` || source == `[{"a":data}][0].a=data`) {
				continue
			}
			binding := hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)
			if format == "" {
				binding = hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil {
				t.Fatal(source, err)
			}
			var rows, types, args []string
			for _, arg := range q.Args() {
				typ, lit := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, lit)
			}
			for i, v := range values {
				if format == "" && v.Kind() != hff.StringKind && v.Kind() != hff.NullKind {
					continue
				}
				oracle := v
				if text, ok := v.AsString(); ok {
					// Rejected native Float input invalidates the whole
					// parsed document, including nested numbers. Preserve
					// the original SQL input while deriving its null result.
					switch text {
					case "-1e-9999999999999999999", "1e-400", "2.4703282292062327e-324", `{"a":-1e-999999}`:
						oracle = hff.String("bad")
					}
				}
				want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"data": oracle}))
				if err != nil {
					t.Fatal(source, i, err)
				}
				row := "(" + sqlFixtureString(metadataJSONFixture(t, v)) + "::" + string(format) + ")"
				if format == "" {
					row = "(" + sqlFixtureValue(t, v, "text") + ")"
				}
				rows = append(rows, row)
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s %s input%d", source, format, i))
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE parse_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data); EXECUTE parse_%d_%d%s;\n", fi, si, parameters, q.Where(), strings.Join(rows, ","), fi, si, arguments)
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
		t.Fatalf("got%d want%d: %s", len(results), len(expected), out)
	}
	for i, r := range results {
		if (r == "t") != expected[i] {
			t.Errorf("%s: SQL=%s expected=%v", labels[i], r, expected[i])
		}
	}
	t.Logf("compared %d strict JSON parsing results", len(results))
}
