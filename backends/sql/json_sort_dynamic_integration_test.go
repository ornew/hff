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

func TestPostgreSQLDynamicSortParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	numbers := schema.Union(schema.IntType, schema.FloatType, schema.NullType)
	types := []hff.Type{
		schema.AnyType,
		schema.Union(schema.ListOf(schema.AnyType), schema.NullType),
		schema.Union(schema.ListOf(numbers), schema.NullType),
		schema.Union(schema.ListOf(numbers), schema.StringType, schema.MapOf(schema.BytesType), schema.NullType),
		schema.ListOf(schema.Union(numbers, schema.BytesType)),
	}
	values := []hff.Value{hff.Null(), hff.String(""), hff.Int(1), hff.Bool(true), hff.Object(nil), hff.List(), hff.List(hff.Null()), hff.List(hff.Bool(true)), hff.List(hff.Object(nil)), hff.List(hff.Int(1), hff.String("1")), hff.List(hff.String("日"), hff.String("é"), hff.String("a")), hff.List(hff.Int(3), hff.Float(1), hff.Int(2)), hff.List(hff.Float(1), hff.Float(math.Copysign(0, -1))), hff.List(hff.Float(1), hff.Int(1)), hff.List(hff.Int(1), hff.Float(1)), hff.List(hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0)), hff.List(hff.Int(9007199254740993), hff.Float(9007199254740992)), hff.List(hff.Float(float64(math.MaxInt64)), hff.Int(math.MaxInt64)), hff.List(hff.Int(math.MinInt64), hff.Float(float64(math.MinInt64))), hff.List(hff.Float(math.SmallestNonzeroFloat64), hff.Int(0), hff.Float(-math.SmallestNonzeroFloat64))}
	// Exercise native JSON spellings that a typed host serializer canonicalizes.
	rawStart := len(values)
	values = append(values,
		hff.List(hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0), hff.Float(1), hff.Int(1)),
		hff.List(hff.String("日"), hff.String("a")),
	)
	rawJSON := []string{` [ -1e-400, 0, 0.0, 1e0, 1 ] `, ` [ "\u65e5", "a" ] `}
	sources := []string{`data:exists=[]:sort:exists`, `data:sort:json="[-0.0,0,0.0,1.0,1]"`, `data:sort`, `data:sort=null`, `data:sort:json="null"`, `data:sort:json="[]"`, `data:sort=[1,2,3]`, `data:sort[0]:[int]:exists`, `data:sort[0]:[float]:exists`, `data:sort[0]<0`, `data:sort:json="[-0.0,0,0.0]"`, `data:sort=[9007199254740992.0,9007199254740993]`, `data!:sort:exists`, `!data!:sort`, `data:sort:sort:json=data:sort:json`, `data:sort:map(v=>v:[float]):json="[-0.0,1.0]"`, `data:sort:map(v=>v:[float]):exists`}
	var script strings.Builder
	script.WriteString("SET jit=off;\n")
	var wants []bool
	var labels []string
	for ti, typ := range types {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for si, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...)))
				if err != nil {
					t.Fatal(typ.Key(), source, err)
				}
				var parameterTypes, args, rows []string
				for _, arg := range q.Args() {
					typ, lit := sqlFixtureParameter(t, arg)
					parameterTypes = append(parameterTypes, typ)
					args = append(args, lit)
				}
				for i, value := range values {
					record := hff.Object(map[string]hff.Value{"data": value})
					if _, err := e.Validate(ctx, record); err != nil {
						continue
					}
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(err)
					}
					jsonText := metadataJSONFixture(t, value)
					if format == hffsql.JSON && i >= rawStart {
						jsonText = rawJSON[i-rawStart]
						if i == rawStart {
							// Go decodes raw -1e-400 as signed zero; PostgreSQL's
							// native Float input model yields null. Its sort oracle
							// therefore uses a decoded null element. The JSONB
							// fixture retains its valid canonical negative zero.
							decoded := hff.Object(map[string]hff.Value{"data": hff.List(hff.Null(), hff.Int(0), hff.Float(0), hff.Float(1), hff.Int(1))})
							want, err = f.Match(ctx, decoded)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					rows = append(rows, "("+sqlFixtureString(jsonText)+"::"+string(format)+","+sqlFixtureString(pairTypeMetadata(t, value))+"::jsonb)")
					wants = append(wants, want)
					labels = append(labels, fmt.Sprintf("type%d %s %s input%d", ti, format, source, i))
				}
				parameters, arguments := "", ""
				if len(parameterTypes) > 0 {
					parameters = "(" + strings.Join(parameterTypes, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE sort_%d_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data,meta); EXECUTE sort_%d_%d_%d%s;\n", ti, fi, si, parameters, q.Where(), strings.Join(rows, ","), ti, fi, si, arguments)
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
	if len(results) != len(wants) {
		t.Fatalf("got%d want%d: %s", len(results), len(wants), out)
	}
	for i, result := range results {
		if (result == "t") != wants[i] {
			t.Errorf("%s: SQL=%s oracle=%v", labels[i], result, wants[i])
		}
	}
	t.Logf("compared %d dynamic sort results", len(results))
}
