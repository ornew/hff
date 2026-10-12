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
	"unicode"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLUnicodeCasing(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var points strings.Builder
	for _, entry := range unicode.CaseRanges {
		for r := entry.Lo; r <= entry.Hi; r++ {
			points.WriteRune(rune(r))
		}
	}
	for r := rune(1); r <= unicode.MaxRune; r += 1024 {
		if r < 0xd800 || r > 0xdfff {
			points.WriteRune(r)
		}
	}
	values := []hff.Value{hff.Null(), hff.Int(1), hff.Float(1), hff.Bool(true), hff.List(), hff.Object(nil)}
	for _, s := range []string{"", "ASCII Case", "İıIißẞ", "ΟΣ Σσς", "éE\u0301", "ÅKÅ", "ﬀﬁﬂ", "ǅǄǆ", "日本語🙂", "𐐀𐐨", "\tA\r\n", points.String()} {
		values = append(values, hff.String(s))
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for mode := 0; mode < 4; mode++ {
		typ := schema.AnyType
		if mode == 0 {
			typ = schema.Union(schema.StringType, schema.NullType)
		}
		if mode == 3 {
			typ = schema.Union(schema.StringType, schema.IntType, schema.NullType)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for mi, method := range []string{"lower", "upper"} {
			for si, suffix := range []string{"=expected", ":string=expected", ":exists", ":[string]:exists", "!:exists", ":type=\"null\"", "callback"} {
				source := "data:" + method + suffix
				if suffix == "callback" {
					source = "[data]:map(x=>x:" + method + ")[0]:string=expected"
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
				if mode > 0 {
					if mode > 1 {
						format = hffsql.JSONB
					}
					var options []hffsql.JSONBindingOption
					if mode == 2 && suffix == "callback" {
						options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
					}
					binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...)
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
					result := "null"
					if s, ok := v.AsString(); ok {
						if method == "lower" {
							result = strings.ToLower(s)
						} else {
							result = strings.ToUpper(s)
						}
					}
					record := hff.Object(map[string]hff.Value{"data": v, "expected": hff.String(result)})
					if _, err := e.Validate(ctx, record); err != nil {
						continue
					}
					want, err := f.Match(ctx, record)
					if err != nil {
						t.Fatal(err)
					}
					if (si == 1 || suffix == "callback") && !want {
						t.Fatalf("local oracle mismatch %s input%d", source, vi)
					}
					data := sqlFixtureString(metadataJSONFixture(t, v)) + "::" + string(format)
					if mode == 0 {
						data = sqlFixtureValue(t, v, "text")
					}
					rows = append(rows, "("+data+","+sqlFixtureString(result)+"::text,"+sqlFixtureString(pairTypeMetadata(t, v))+"::jsonb)")
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("mode%d %s input%d", mode, source, vi))
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE casing_%d_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data,expected,meta); EXECUTE casing_%d_%d_%d%s;\n", mode, mi, si, parameters, q.Where(), strings.Join(rows, ","), mode, mi, si, arguments)
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
			t.Errorf("%s: SQL=%s local=%v", labels[i], r, expected[i])
		}
	}
	t.Logf("compared %d Unicode casing results", len(results))
}
