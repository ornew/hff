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

func TestPostgreSQLPrimitiveStringConversions(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stringsIn := []string{"true", "false", "TRUE", "False", " true", "false ", "true\n", "yes", "on", "", " ", "1", "0", "-0", "1.0", "-0.0", "1e0", "1E+6", "-5", "5.25", "+1", "01", "-01", ".5", "1.", "1e", "1e-foo", "1e-99999999999999999999", "-1e-99999999999999999999", "1e999999", "1e-400", "5e-324", "2.4703282292062327e-324", "2.4703282292062328e-324", "9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809", "9007199254740993", "9007199254740993.0", "1.7976931348623157e308", "1.7976931348623159e308", "NaN", "Infinity", "-Infinity", "0x10", "１", "١", "1\n", "1 ", "\t1", "1\r", "0.0", "-0e1", `["1.0","-0.0",1.0]`, `["1.0","-1.0"]`, `["true","false",true]`}
	values := []hff.Value{hff.Null(), hff.Int(1), hff.Bool(true), hff.List(), hff.Object(nil), hff.Float(1)}
	for _, s := range stringsIn {
		values = append(values, hff.String(s))
	}
	sources := []string{`data:bool=null`, `data:bool=true`, `data:bool=false`, `data:bool:json="null"`, `data:bool:json="false"`, `data!:bool:json="null"`, `!data:bool`, `data:bool:and(true)`, `data:bool:and((null!):bool):exists`, `data:bool:or((null!):bool):exists`, `data:bool:and((null!):bool):json="null"`, `data:number=null`, `data:number:[int]=1`, `data:number:[float]=1`, `data:number:json="1.0"`, `data:number:string="-0.0"`, `data:number=0`, `data:number>0`, `data:number%2=1`, `(data:number%2):type="null"`, `(data:number+1):json="2.0"`, `(data:number+1):json="2"`, `data!:number:exists`, `data:number:type="number"`, `data:number:[float]`, `data:bool:type=(null:bool).a:type`, `data:number:type=(null:number)[0]:type`, `data:number=("1e-foo":number)`, `data:bool=("TRUE":bool)`, `[data:number]:json="[1.0]"`, `data:number:json=data`, `data:fromjson:[list]:map(x=>x:number):json="[1.0,-0.0,null]"`, `data:fromjson:[list]:foldl(0,(a,x)=>a+x:number):json="0.0"`, `data:fromjson:[list]:map(x=>x:bool):json="[true,false,null]"`}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB, "", hffsql.JSONB} {
		typ := schema.AnyType
		if format == "" {
			typ = schema.Union(schema.StringType, schema.NullType)
		}
		if fi == 3 {
			typ = schema.Union(schema.StringType, schema.FloatType, schema.NullType)
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
				if fi == 3 {
					if _, err := e.Validate(ctx, hff.Object(map[string]hff.Value{"data": v})); err != nil {
						continue
					}
				}
				oracle := v
				if text, ok := v.AsString(); ok {
					// PostgreSQL rejects these underflow spellings. Use an
					// invalid conversion receiver for this profile-specific
					// oracle; raw SQL input still contains the original text.
					switch text {
					case "1e-99999999999999999999", "-1e-99999999999999999999", "1e-400", "2.4703282292062327e-324":
						oracle = hff.String("NaN")
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
			fmt.Fprintf(&script, "PREPARE conversion_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data); EXECUTE conversion_%d_%d%s;\n", fi, si, parameters, q.Where(), strings.Join(rows, ","), fi, si, arguments)
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
	t.Logf("compared %d strict string conversion results", len(results))
}
