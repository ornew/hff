//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
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

func TestPostgreSQLLiteralRegexParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	patterns := []string{`[^\s\S]`, `[^\d\D]`, "", "^$", "^A", "^Alice$", "A|B", "(A|B)+", "A?", "A*", "(?:|A)", "a{0,255}", "a{255,}", "a{2,3}?", `[a-zA-Z0-9_]`, `\d+`, `\s`, `\w+`, `[\]\[\-\\\^]`, `\$`, `\Aabc\z`, `\n`, "[", `\C`, `a{1001}`}
	values := []hff.Value{hff.Null(), hff.String(""), hff.String("Alice"), hff.String("Alice\n"), hff.String("prefixAlice"), hff.String("a\nb"), hff.String("abc"), hff.String("abc\n"), hff.String("AB"), hff.String("éA日"), hff.String("١"), hff.String("\t"), hff.String("$[-\\^]"), hff.String(strings.Repeat("a", 256))}
	var script strings.Builder
	var wants []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{"", hffsql.JSON, hffsql.JSONB} {
		typ := schema.Union(schema.StringType, schema.NullType)
		inputs := values
		if format != "" {
			typ = schema.AnyType
			inputs = append(append([]hff.Value{}, values...), hff.Int(1), hff.Bool(true), hff.List(), hff.Object(nil))
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		var sources []string
		for _, pattern := range patterns {
			quoted, _ := json.Marshal(pattern)
			for _, expression := range []string{"data~" + string(quoted), "data!~" + string(quoted), "data:matches(" + string(quoted) + ")", "data!:matches(" + string(quoted) + ")", "!data!:matches(" + string(quoted) + ")", "data:matches(" + string(quoted) + "):type=\"null\"", "data!~" + string(quoted) + "=null"} {
				// Comparisons cannot chain, so observe nullable regex results
				// through a parenthesized value expression.
				if strings.HasSuffix(expression, "=null") {
					expression = "(data!~" + string(quoted) + ")=null"
				}
				sources = append(sources, expression)
			}
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
			binding := hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))
			if format != "" {
				binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil {
				t.Fatal(source, err)
			}
			var types, args, rows []string
			for _, arg := range q.Args() {
				typ, lit := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, lit)
			}
			for i, value := range inputs {
				want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"data": value}))
				if err != nil {
					t.Fatal(err)
				}
				literal := ""
				if format == "" {
					literal = sqlFixtureValue(t, value, "text")
				} else {
					literal = sqlFixtureString(metadataJSONFixture(t, value)) + "::" + string(format)
				}
				rows = append(rows, "("+literal+")")
				wants = append(wants, want)
				labels = append(labels, fmt.Sprintf("%s %s input%d", source, format, i))
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE regex_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data); EXECUTE regex_%d_%d%s;\n", fi, si, parameters, q.Where(), strings.Join(rows, ","), fi, si, arguments)
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
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, wants[i])
		}
	}
	t.Logf("compared %d literal regex results", len(results))
}
