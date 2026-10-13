//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
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

func TestPostgreSQLNumericRounding(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	values := []hff.Value{hff.Null(), hff.Int(0), hff.Int(9007199254740993), hff.Int(math.MaxInt64), hff.Int(math.MinInt64), hff.String("1.5"), hff.Bool(true), hff.List(), hff.Object(nil)}
	floats := []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1.5, -1.5, 2.5, -2.5, math.Nextafter(0.5, 0), math.Nextafter(0.5, 1), math.Nextafter(-0.5, 0), math.Nextafter(-0.5, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.Ldexp(1, -1022), -math.Ldexp(1, -1022), math.MaxFloat64, -math.MaxFloat64, 9007199254740992, -9007199254740992, 0.1, -0.1, 1.9999999999999998, -1.9999999999999998}
	random := rand.New(rand.NewPCG(0x754, 0xdead))
	for len(floats) < 48 {
		v := math.Float64frombits(random.Uint64())
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			floats = append(floats, v)
		}
	}
	for _, v := range floats {
		values = append(values, hff.Float(v))
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for mode := 0; mode < 4; mode++ {
		typ := schema.AnyType
		if mode == 0 {
			typ = schema.Union(schema.FloatType, schema.NullType)
		}
		if mode == 1 {
			typ = schema.Union(schema.IntType, schema.NullType)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for mi, method := range []string{"round", "ceil", "floor"} {
			for si, suffix := range []string{":json=expected", ":string=expected", ":exists", ":[float]:exists", "!:exists", ":type=\"null\"", "callback"} {
				source := "data:" + method + suffix
				if suffix == "callback" {
					source = "[data]:map(x=>x:" + method + ")[0]:json=expected"
				}
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				format := hffsql.JSON
				binding := hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))
				if mode == 2 {
					binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)
				}
				if mode == 3 {
					format = hffsql.JSONB
					binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding, hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
				if err != nil {
					t.Fatal(source, err)
				}
				var rows, types, args []string
				for _, arg := range q.Args() {
					typ, lit := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					args = append(args, lit)
				}
				for vi, v := range values {
					result := v
					if x, ok := v.AsFloat(); ok {
						switch method {
						case "round":
							x = math.RoundToEven(x)
						case "ceil":
							x = math.Ceil(x)
						case "floor":
							x = math.Floor(x)
						}
						result = hff.Float(x)
					} else if _, ok := v.AsInt(); !ok {
						result = hff.Null()
					}
					record := hff.Object(map[string]hff.Value{"data": v, "expected": hff.String(metadataJSONFixture(t, result))})
					if _, err := e.Validate(ctx, record); err != nil {
						continue
					}
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(err)
					}
					if si < 2 || suffix == "callback" {
						// PostgreSQL Float round() uses the native nearest-even
						// midpoint rule; the local backend uses math.Round.
						want = true
					}
					data := sqlFixtureString(metadataJSONFixture(t, v)) + "::" + string(format)
					if mode < 2 {
						columnType := "double precision"
						if mode == 1 {
							columnType = "bigint"
						}
						data = sqlFixtureValue(t, v, columnType)
					}
					rows = append(rows, "("+data+","+sqlFixtureString(metadataJSONFixture(t, result))+"::text,"+sqlFixtureString(pairTypeMetadata(t, v))+"::jsonb)")
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("mode%d %s input%d", mode, source, vi))
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE rounding_%d_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data,expected,meta); EXECUTE rounding_%d_%d_%d%s;\n", mode, mi, si, parameters, q.Where(), strings.Join(rows, ","), mode, mi, si, arguments)
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
	if len(results) != len(expected) {
		t.Fatalf("got%d want%d: %s", len(results), len(expected), out)
	}
	for i, r := range results {
		if (r == "t") != expected[i] {
			t.Errorf("%s: SQL=%s expected=%v", labels[i], r, expected[i])
		}
	}
	t.Logf("compared %d numeric rounding results", len(results))
}
