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

func TestPostgreSQLDynamicCanonicalJSON(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	values := []hff.Value{hff.Null(), hff.Bool(false), hff.Bool(true), hff.Int(-9223372036854775808), hff.Int(9223372036854775807), hff.Float(1), hff.Float(math.Copysign(0, -1)), hff.Float(5e-324), hff.Float(1e20), hff.String(""), hff.String("Alice"), hff.List(), hff.Object(nil), hff.List(hff.Int(1), hff.Float(1), hff.Null(), hff.Bool(false)), hff.Object(map[string]hff.Value{"z": hff.List(hff.Float(math.Copysign(0, -1)), hff.Object(nil)), "a": hff.String("東京")})}
	deep := hff.Float(1)
	for range 32 {
		deep = hff.List(deep)
	}
	values = append(values, deep)
	special := hff.String("<>&\u2028\u2029\n\t\"\\")
	values = append(values, special, hff.Object(map[string]hff.Value{"<>&": special, "\n": hff.Int(1)}))
	var script strings.Builder
	var labels []string
	for ti, typ := range []hff.Type{schema.AnyType, schema.Union(schema.IntType, schema.FloatType, schema.StringType, schema.NullType), schema.Union(schema.StringType, schema.IntType), schema.ListOf(schema.AnyType), schema.MapOf(schema.AnyType)} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for mi, method := range []string{"json", "string", "array", "object", "callback"} {
				source := "data:" + method + "=expected"
				if method == "callback" {
					source = `data:[list]:map(x=>x):json=expected`
				}
				if method == "array" {
					source = "[data]:json=expected"
				}
				if method == "object" {
					source = `{"v":data}:json=expected`
				}
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				options := []hffsql.JSONBindingOption{}
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...), hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
				if err != nil {
					t.Fatal(err)
				}
				var rows []string
				for i, v := range values {
					if method == "callback" && v.Kind() != hff.ListKind {
						continue
					}
					if _, err := e.Validate(ctx, hff.Object(map[string]hff.Value{"data": v, "expected": hff.String("")})); err != nil {
						continue
					}
					want := metadataJSONFixture(t, v)
					if i == len(values)-2 {
						want = "\"<>&\u2028\u2029\\n\\t\\\"\\\\\""
					}
					if i == len(values)-1 {
						want = "{\"\\n\":1,\"<>&\":\"<>&\u2028\u2029\\n\\t\\\"\\\\\"}"
					}
					if method == "array" {
						want = "[" + want + "]"
					}
					if method == "object" {
						want = `{"v":` + want + "}"
					}
					if method == "string" {
						if text, ok := v.AsString(); ok {
							want = text
						}
					}
					match, err := f.Match(ctx, hff.Object(map[string]hff.Value{"data": v, "expected": hff.String(want)}))
					if err != nil || !match {
						t.Fatalf("local %s case%d: %v", source, i, err)
					}
					rows = append(rows, "("+sqlFixtureString(" \n"+metadataJSONFixture(t, v)+"\t ")+"::"+string(format)+","+sqlFixtureString(pairTypeMetadata(t, v))+"::jsonb,"+sqlFixtureString(want)+"::text)")
					labels = append(labels, fmt.Sprintf("type%d %s %s case%d", ti, source, format, i))
				}
				if len(rows) == 0 {
					continue
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
				fmt.Fprintf(&script, "PREPARE canon_%d_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data,meta,expected); EXECUTE canon_%d_%d_%d%s;\n", ti, fi, mi, parameters, q.Where(), strings.Join(rows, ","), ti, fi, mi, arguments)

			}
		}
	}
	if len(labels) != 324 {
		t.Fatalf("fixture selection changed: got %d comparisons, want 324", len(labels))
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(labels) {
		t.Fatalf("got%d want%d: %s", len(results), len(labels), out)
	}
	t.Logf("received %d canonical serialization comparisons", len(results))
	for i, r := range results {
		if r != "t" {
			t.Errorf("%s: SQL=%s", labels[i], r)
		}
	}
}
