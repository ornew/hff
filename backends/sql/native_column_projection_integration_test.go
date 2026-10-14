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

func TestPostgreSQLQualifiedNativeColumnScopes(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Int("i")), schema.Nullable(schema.Int("j")),
		schema.Nullable(schema.Float("f")), schema.String("s"), schema.Bool("p"),
		schema.Bytes("b"), schema.Duration("d"), schema.List("items", schema.IntType),
		schema.Field("flat", schema.Struct(schema.Int("age"))),
		schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(schema.Int("age"))))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	// Caller-owned aliases and physical names overlap the generator's usual
	// names. Quoting must preserve literal components rather than parse dots.
	mappings := []hffsql.Binding{
		hffsql.Bind(hff.Path{"i"}, hffsql.Column("__hff_1", "v")),
		hffsql.Bind(hff.Path{"j"}, hffsql.Column("__hff_1", "p")),
		hffsql.Bind(hff.Path{"f"}, hffsql.Column("__hff_1", "value")),
		hffsql.Bind(hff.Path{"s"}, hffsql.Column("__hff_1", `odd".column`)),
		hffsql.Bind(hff.Path{"p"}, hffsql.Column("__hff_1", "flag")),
		hffsql.Bind(hff.Path{"b"}, hffsql.Column("__hff_1", "bytes")),
		hffsql.Bind(hff.Path{"d"}, hffsql.Column("__hff_1", "ns")),
		hffsql.Bind(hff.Path{"flat", "age"}, hffsql.Column("__hff_1", "ord")),
		hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("__hff_1", "items"), hffsql.JSON),
		hffsql.BindStruct(hff.Path{"profile"},
			hffsql.TrustedSQL(`"__hff_1"."profile_present"`, hffsql.Column("__hff_1", "profile_present")),
			hffsql.TrustedSQL(`"__hff_1"."profile_null"`, hffsql.Column("__hff_1", "profile_null")),
			hffsql.Bind(hff.Path{"age"}, hffsql.Column("__hff_1", "age"))),
	}
	var script strings.Builder
	script.WriteString(`CREATE TEMP TABLE native_scope(id integer,v bigint,p bigint,value double precision,"odd"".column" text,flag boolean,bytes bytea,ns bigint,ord bigint,items json,profile_present boolean,profile_null boolean,age bigint);` + "\n")
	var records []hff.Value
	values := []hff.Value{hff.Int(-2), hff.Int(0), hff.Int(7), hff.Null()}
	for li, left := range values {
		for ri, right := range values {
			for state := range 4 {
				index := len(records)
				integer, valid := left.AsInt()
				floating := hff.Null()
				if valid {
					floating = hff.Float(float64(integer) + 0.5)
				}
				fields := map[string]hff.Value{
					"i": left, "j": right, "f": floating, "s": hff.String("世界"), "p": hff.Bool(index%2 == 0),
					"b": hff.Bytes([]byte{byte(index)}), "d": hff.Duration(time.Duration(integer)),
					"items": hff.List(hff.Int(-1), hff.Int(0), hff.Int(1)),
					"flat":  hff.Object(map[string]hff.Value{"age": hff.Int(int64(li + ri))}),
				}
				if state == 1 {
					fields["profile"] = hff.Null()
				} else if state >= 2 {
					fields["profile"] = hff.Object(map[string]hff.Value{"age": hff.Int(int64(state - 2))})
				}
				records = append(records, hff.Object(fields))
				fmt.Fprintf(&script, "INSERT INTO native_scope VALUES(%d,%s,%s,%s,%s,%s,%s,%s,%d,'[-1,0,1]'::json,%t,%t,%d);\n", index,
					sqlFixtureValue(t, left, "bigint"), sqlFixtureValue(t, right, "bigint"), sqlFixtureValue(t, floating, "double precision"),
					sqlFixtureValue(t, fields["s"], "text"), sqlFixtureValue(t, fields["p"], "boolean"), sqlFixtureValue(t, fields["b"], "bytea"),
					sqlFixtureValue(t, fields["d"], "bigint"), li+ri, state != 0, state == 1, state-2)
			}
		}
	}
	sources := []string{
		`i+1>j`, `i=f`, `!(i=f)`, `i!:exists`, `i!:type="null"`, `i:[int]:exists`,
		`i:json=i:json`, `[i,i]!=[]`, `f:round>1`, `s:lower=s`, `p:and(p):type="bool"`,
		`b:len>i`, `d:nanoseconds:json=i:floor:json`, `flat.age+1>i`,
		`profile.age:exists`, `profile!.age:exists`, `profile.age=null`, `profile!.age=null`,
		`items:any(x=>i:exists)`, `items:any(i=>i>j)`, `items:any(i=>items:any(i=>i>j))`,
		`items:map(x=>i+x):size>0`, `items:foldl(i,(i,x)=>i+x)>0`,
		`items:any(x=>i!+x>j)`, `i+1>0 j+2<10 s="世界"`,
	}
	type scopeCase struct{ source, unqualified string }
	var cases []scopeCase
	for _, source := range sources {
		cases = append(cases, scopeCase{source: source})
	}
	cases = append(cases, scopeCase{`i+1>0`, "v"}, scopeCase{`i+1>0`, "p"}, scopeCase{`i!:exists`, "v"}, scopeCase{`i!:exists`, "p"})
	var wants []bool
	var labels []string
	for index, fixture := range cases {
		source := fixture.source
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		f, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(source, err)
		}
		bindings := append([]hffsql.Binding(nil), mappings...)
		if fixture.unqualified != "" {
			bindings[0] = hffsql.Bind(hff.Path{"i"}, hffsql.Column(fixture.unqualified))
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		for ri, record := range records {
			if fixture.unqualified == "p" {
				fields, _ := record.AsObject()
				fields["i"] = fields["j"]
				record = hff.Object(fields)
			}
			want, err := f.Match(ctx, record)
			if err != nil {
				t.Fatal(source, ri, err)
			}
			wants, labels = append(wants, want), append(labels, fmt.Sprintf("%s unqualified=%s row%d", source, fixture.unqualified, ri))
		}
		var types, arguments []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, arguments = append(types, typ), append(arguments, literal)
		}
		params, args := "", ""
		if len(types) > 0 {
			params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(arguments, ",")+")"
		}
		fmt.Fprintf(&script, "PREPARE column_scope_%d%s AS SELECT COALESCE((%s)::text,'n') FROM native_scope AS \"__hff_1\" ORDER BY id; EXECUTE column_scope_%d%s;\n", index, params, q.Where(), index, args)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(wants) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(wants), out)
	}
	for index, result := range results {
		if (result != "true" && result != "false") || (result == "true") != wants[index] {
			t.Errorf("%s: SQL=%s local=%t", labels[index], result, wants[index])
		}
	}
	t.Logf("compared %d qualified native-column scope results", len(results))
}
