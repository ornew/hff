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

func TestPostgreSQLArithmeticIdentitiesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, options := identityEngine(t)
	var records []hff.Value
	var rows []string
	ints := []int64{math.MinInt64, -9007199254740993, -1, 0, 1, 18, 19, 9007199254740993, math.MaxInt64}
	floats := []float64{-math.MaxFloat64, -math.SmallestNonzeroFloat64, math.Copysign(0, -1), 0, math.SmallestNonzeroFloat64, 1, 18, 19, math.MaxFloat64}
	for i := 0; i <= len(ints); i++ {
		age, ratio, dynamic := hff.Null(), hff.Null(), hff.Null()
		ageSQL, ratioSQL := "NULL::bigint", "NULL::double precision"
		if i < len(ints) {
			age, ratio = hff.Int(ints[i]), hff.Float(floats[i])
			dynamic = age
			if i%2 == 0 {
				dynamic = ratio
			}
			ageSQL, ratioSQL = fmt.Sprintf("'%d'::bigint", ints[i]), fmt.Sprintf("'%g'::double precision", floats[i])
		}
		items := hff.List(ratio, hff.Null(), hff.Float(1))
		records = append(records, hff.Object(map[string]hff.Value{"age": age, "ratio": ratio, "value": dynamic, "items": items, "name": hff.String("x")}))
		rows = append(rows, "("+ageSQL+","+ratioSQL+","+sqlFixtureString(metadataJSONFixture(t, dynamic))+"::json,"+sqlFixtureString(metadataJSONFixture(t, items))+"::json,'x'::text)")
	}
	var sources []string
	for _, expression := range []string{
		`age+0`, `(0+age)`, `age-0`, `age*1`, `(1*age)`, `age*"1":number`,
		`age+0.0`, `(0.0+age)`, `age-0.0`, `age*1.0`, `(1.0*age)`, `age/1`, `age/1.0`,
		`ratio-0`, `ratio-0.0`, `ratio*1`, `(1*ratio)`, `ratio*1.0`, `(1.0*ratio)`, `ratio/1`, `ratio/1.0`,
		`age!*1`, `ratio!*1.0`, `value:[int]*1`, `value:[float]/1`,
		`age*1*1+0`, `ratio*1.0/1.0-0.0`,
		// These deliberately remain general: signed zero and runtime families.
		`ratio+0.0`, `(0.0+ratio)`, `ratio-(-0.0)`, `value*1`, `value/1`, `age*0`,
	} {
		for _, suffix := range []string{`>18`, `=null`, `!=null`, `:exists`, `:type="int"`, `:type="float"`} {
			source := "(" + expression + ")" + suffix
			sources = append(sources, source, "!("+source+")")
		}
	}
	sources = append(sources,
		`age+0=age`, `age+0.0=age`, `age/1=age`,
		`(ratio*1.0):json=ratio:json`, `(ratio/1):json=ratio:json`, `(ratio-0.0):json=ratio:json`,
		`age:in(age+0,age*1)`, `(age*1):in(18,19)`,
		`items:any(x=>x*1.0>18)`, `items:all(x=>(x/1):exists)`,
		`items:map(x=>x*1)=items`, `items:map(x=>x/1)=items`,
		`items:map(x=>age*1)=[age,age,age]`,
		`items:map(age=>age*1)=items`,
		`items:map(x=>items:map(x=>x*1))=[items,items,items]`,
		`items:foldl(0.0,(acc,x)=>(acc*1.0)+(x*1.0))>18`,
		`(age!*1)=null|(ratio!*1.0)>18`,
		`name=0:string (age>("1":number*1))`,
	)
	var script strings.Builder
	var expected []bool
	var labels []string
	for si, source := range sources {
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
		var types, args []string
		for _, value := range q.Args() {
			typ, literal := sqlFixtureParameter(t, value)
			types, args = append(types, typ), append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
		}
		fmt.Fprintf(&script, "PREPARE identity_%d%s AS SELECT %s FROM (VALUES %s) records(age,ratio,value,items,name); EXECUTE identity_%d%s;\n", si, parameters, q.Where(), strings.Join(rows, ","), si, arguments)
		for ri, record := range records {
			want, err := f.Match(ctx, record)
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s record%d", source, ri))
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
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d arithmetic identity comparisons", len(expected))
}

func TestPostgreSQLArithmeticIdentityRetainsExtensionCarrier(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, d := foldedNullExtension(t)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Int("age")), schema.Nullable(schema.Float("ratio")), schema.String("name"))), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	cases := []struct{ expression, carrier string }{
		{`age+0`, "bigint"}, {`(0+age)`, "bigint"}, {`age*1`, "bigint"},
		{`age+0.0`, "double precision"}, {`age*1.0`, "double precision"}, {`age/1`, "double precision"},
		{`ratio*1`, "double precision"}, {`ratio/1`, "double precision"}, {`ratio-0.0`, "double precision"},
	}
	for _, tc := range cases {
		p, err := e.Compile(ctx, `name:nullcarrier(`+tc.expression+`)`)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")), hffsql.Bind(hff.Path{"ratio"}, hffsql.Column("records", "ratio")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))),
			hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
				return c.Expression(schema.BoolType, "pg_typeof({})::text='"+tc.carrier+"'", args[0])
			})))
		if err != nil {
			t.Fatal(err)
		}
		if len(q.Args()) != 0 {
			t.Fatal(tc.expression, q.Args())
		}
		fmt.Fprintf(&script, "SELECT %s FROM (VALUES (NULL::bigint,NULL::double precision,'x'::text),(18::bigint,18.0::double precision,'x'::text)) records(age,ratio,name);\n", q.Where())
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != 2*len(cases) {
		t.Fatalf("got %d results, want %d: %s", len(results), 2*len(cases), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s lost %s carrier: %s", cases[i/2].expression, cases[i/2].carrier, result)
		}
	}
}
