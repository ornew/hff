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

func TestPostgreSQLNumericExtremesMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	i, f, nilValue := hff.Int, hff.Float, hff.Null()
	zero := f(math.Copysign(0, -1))
	low := int64(1) << 52
	wide := int64(1) << 53
	rows := []struct{ values, minimum, maximum hff.Value }{
		{nilValue, nilValue, nilValue}, {hff.List(), nilValue, nilValue}, {hff.List(nilValue), nilValue, nilValue},
		{hff.List(i(2), nilValue, i(1)), i(1), i(2)},
		{hff.List(i(math.MinInt64), i(math.MaxInt64)), i(math.MinInt64), i(math.MaxInt64)},
		{hff.List(f(2.5), nilValue, f(1.5)), f(1.5), f(2.5)},
		{hff.List(f(1.5), i(2)), f(1.5), i(2)},
		{hff.List(i(1), f(1)), i(1), i(1)}, {hff.List(f(1), i(1)), f(1), f(1)},
		{hff.List(zero, f(0)), zero, zero}, {hff.List(f(0), zero), f(0), f(0)},
		{hff.List(i(0), f(math.SmallestNonzeroFloat64)), i(0), f(math.SmallestNonzeroFloat64)},
		{hff.List(i(1), f(math.Nextafter(1, 0))), f(math.Nextafter(1, 0)), i(1)},
		{hff.List(i(low+1), f(float64(low))), f(float64(low)), i(low + 1)},
		{hff.List(i(low-1), f(math.Nextafter(float64(low), 0))), i(low - 1), f(math.Nextafter(float64(low), 0))},
		{hff.List(i(-low+1), f(math.Nextafter(float64(-low), 0))), f(math.Nextafter(float64(-low), 0)), i(-low + 1)},
		{hff.List(i(wide+1), f(float64(wide))), f(float64(wide)), i(wide + 1)},
		{hff.List(i(-wide-1), f(float64(-wide))), i(-wide - 1), f(float64(-wide))},
		{hff.List(i(math.MaxInt64), f(float64(math.MaxInt64))), i(math.MaxInt64), f(float64(math.MaxInt64))},
		{hff.List(i(math.MaxInt64-1022), f(math.Nextafter(float64(math.MaxInt64), 0))), f(math.Nextafter(float64(math.MaxInt64), 0)), i(math.MaxInt64 - 1022)},
		{hff.List(i(math.MinInt64), f(float64(math.MinInt64))), i(math.MinInt64), i(math.MinInt64)},
		{hff.List(f(float64(math.MinInt64)), i(math.MinInt64)), f(float64(math.MinInt64)), f(float64(math.MinInt64))},
		{hff.List(i(math.MinInt64), f(math.Nextafter(float64(math.MinInt64), math.Inf(-1)))), f(math.Nextafter(float64(math.MinInt64), math.Inf(-1))), i(math.MinInt64)},
		{hff.List(f(1e20), i(math.MaxInt64)), i(math.MaxInt64), f(1e20)},
		{hff.List(f(-1e20), i(math.MinInt64)), f(-1e20), i(math.MinInt64)},
	}
	for _, family := range []struct {
		element    hff.Type
		kind       hff.Kind
		projection bool
	}{
		{schema.Union(schema.IntType, schema.FloatType, schema.NullType), hff.InvalidKind, false},
		{schema.AnyType, hff.InvalidKind, false},
		{schema.NullableType(schema.IntType), hff.IntKind, false},
		{schema.NullableType(schema.FloatType), hff.FloatKind, false},
		{schema.Union(schema.IntType, schema.FloatType, schema.NullType), hff.InvalidKind, true},
		{schema.AnyType, hff.InvalidKind, true},
	} {
		element := family.element
		var cases []struct{ values, minimum, maximum hff.Value }
		for _, row := range rows {
			values, _ := row.values.AsList()
			compatible := true
			for _, value := range values {
				if family.kind != hff.InvalidKind && value.Kind() != hff.NullKind && value.Kind() != family.kind {
					compatible = false
				}
			}
			if compatible {
				cases = append(cases, row)
			}
		}
		if element.Kind() == hff.AnyType {
			cases = append(cases, struct{ values, minimum, maximum hff.Value }{hff.List(i(1), hff.String("bad")), nilValue, nilValue})
		}
		methods := []string{"min", "max", "min(x=>x!)", "max(x=>x!)"}
		if family.projection {
			element = schema.Struct(schema.Field("price", element))
			methods = []string{"min(x=>x.price)", "max(x=>x.price)", "min(x=>x.price!)", "max(x=>x.price!)"}
			for index, row := range cases {
				if row.values.Kind() == hff.NullKind {
					continue
				}
				values, _ := row.values.AsList()
				for index, value := range values {
					values[index] = hff.Object(map[string]hff.Value{"price": value})
				}
				cases[index].values = hff.List(values...)
			}
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(element.Key()+"/"+string(format), func(t *testing.T) {
				e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", element)))), engine.WithCapabilities(capabilities.Standard()))
				if err != nil {
					t.Fatal(err)
				}
				var opts []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				var script strings.Builder
				var labels []string
				for _, row := range cases {
					for _, method := range methods {
						want := row.minimum
						if strings.HasPrefix(method, "max") {
							want = row.maximum
						}
						expr := "items:" + method
						source := extremeFixtureSource(t, expr, want)
						p, err := e.Compile(ctx, source)
						if err != nil {
							t.Fatal(source, err)
						}
						filter, err := local.Build(ctx, p)
						if err != nil {
							t.Fatal(err)
						}
						matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": row.values}))
						if err != nil || !matched {
							t.Fatalf("bad Local fixture %s on %s: %v, %v", source, metadataJSONFixture(t, row.values), matched, err)
						}
						q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, opts...)))
						if err != nil {
							t.Fatal(source, err)
						}
						var types, args []string
						for _, arg := range q.Args() {
							typ, literal := sqlFixtureParameter(t, arg)
							types = append(types, typ)
							args = append(args, literal)
						}
						params, execArgs := "", ""
						if len(types) > 0 {
							params = "(" + strings.Join(types, ",") + ")"
							execArgs = "(" + strings.Join(args, ",") + ")"
						}
						name := fmt.Sprintf("extreme_%d", len(labels))
						fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb)) records(items,metadata); EXECUTE %s%s; DEALLOCATE %s;\n", name, params, q.Where(), sqlFixtureString(metadataJSONFixture(t, row.values)), format, sqlFixtureString(pairTypeMetadata(t, row.values)), name, execArgs, name)
						labels = append(labels, source+" on "+metadataJSONFixture(t, row.values))
					}
				}
				command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				command.Stdin = strings.NewReader(script.String())
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("PostgreSQL extrema failed: %v\n%s", err, output)
				}
				results := strings.Fields(string(output))
				if len(results) != len(labels) {
					t.Fatalf("got %d results, want %d: %s", len(results), len(labels), output)
				}
				for index, result := range results {
					if result != "t" {
						t.Errorf("%s: PostgreSQL=%s", labels[index], result)
					}
				}
			})
		}
	}
}

func extremeFixtureSource(t testing.TB, expr string, want hff.Value) string {
	t.Helper()
	if want.Kind() == hff.NullKind {
		return expr + "=null"
	}
	typ := "int"
	if want.Kind() == hff.FloatKind {
		typ = "float"
		v, _ := want.AsFloat()
		if v == 0 {
			return expr + `:json="` + metadataJSONFixture(t, want) + `"`
		}
	}
	return expr + "=" + metadataJSONFixture(t, want) + " " + expr + ":[" + typ + "]:exists"
}

func TestPostgreSQLOrderedFloatRelationExtremeTies(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("hff_extremes"), "child"),
		hffsql.WithCorrelation(hffsql.TrustedSQL("true")),
		hffsql.WithRelationItem(schema.FloatType, hffsql.Bind(hff.Path{}, hffsql.Column("child", "value"))),
		hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("child", "id"), schema.IntType)))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_extremes(id bigint,value double precision); INSERT INTO hff_extremes VALUES (2,'0'::double precision),(1,'-0'::double precision);\n")
	for index, source := range []string{`items:min:json="-0.0"`, `items:max:json="-0.0"`, `items:min(x=>x):json="-0.0"`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		fmt.Fprintf(&script, "PREPARE tie%d(%s) AS SELECT %s; EXECUTE tie%d(%s); DEALLOCATE tie%d;\n", index, strings.Join(types, ","), q.Where(), index, strings.Join(args, ","), index)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ordered relation extreme failed: %v\n%s", err, output)
	}
	if got := strings.Fields(string(output)); strings.Join(got, ",") != "t,t,t" {
		t.Fatalf("first signed-zero tie was not retained: %s", output)
	}
}
