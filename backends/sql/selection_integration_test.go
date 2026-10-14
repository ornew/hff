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

func TestPostgreSQLListSelectionMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	i, f, n := hff.Int, hff.Float, hff.Null()
	obj := func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"n": v}) }
	for _, tc := range []struct {
		name    string
		element hff.Type
		rows    []hff.Value
		sources []string
	}{
		{"integer", schema.NullableType(schema.IntType), []hff.Value{n, hff.List(), hff.List(i(1), n, i(1), i(2)), hff.List(i(math.MaxInt64), i(math.MinInt64), i(math.MaxInt64))}, []string{`items:distinct:size=3`, `items:distinct:unique`, `items:find(v=>v>1)=2`, `items:find(v=>true)=null`, `items:find(v=>v!)=1`}},
		{"float", schema.NullableType(schema.FloatType), []hff.Value{n, hff.List(), hff.List(f(math.Copysign(0, -1)), f(0), f(1.5)), hff.List(f(1.5), f(1.5))}, []string{`items:distinct:size=2`, `items:distinct[0]:json="-0.0"`, `items:find(v=>true):json="-0.0"`, `items:find(v=>v>0)=1.5`}},
		{"mixed", schema.Union(schema.IntType, schema.FloatType, schema.NullType), []hff.Value{n, hff.List(), hff.List(i(1), f(1), n, f(math.Copysign(0, -1)), i(0)), hff.List(f(1), i(1)), hff.List(i(9007199254740993), f(9007199254740992)), hff.List(i(math.MinInt64), f(-0x1p63))}, []string{`items:distinct:size=2`, `items:distinct:size=3`, `items:distinct[0]:[float]:exists`, `items:distinct:unique`, `items:find(v=>true):[float]:exists`, `items:find(v=>v>9007199254740992)=9007199254740993`, `items:find(v=>v!)=1`}},
		{"any", schema.AnyType, []hff.Value{n, hff.List(), hff.List(n, hff.Bool(false), i(0), hff.String("0"), n), hff.List(hff.List(i(1), n), hff.List(f(1), n), obj(i(1)), obj(f(1))), hff.List(hff.Object(nil), obj(n), hff.Object(nil)), hff.List(hff.String("あ"), hff.String("い"), hff.String("あ"))}, []string{`items:distinct:size=4`, `items:distinct:size=2`, `items:distinct:unique`, `items:find(v=>v:exists)=false`, `items:find(v=>true)=null`, `items:find(v=>v!)=null`, `items:find(v=>v.n>0)={"n":1}`, `items:map(v=>[v!]):distinct:json="[]"`, `items:map(v=>[v!]):find(v=>true):json="[]"`, `items:map(v=>[v!]):distinct=[]`, `items:map(v=>[v!]):find(v=>true)=[]`, `items:map(v=>[v!]):distinct=[["あ"],["い"]]`, `items:map(v=>[v!]):find(v=>true)=["あ"]`}},
		{"object", schema.Struct(schema.Nullable(schema.Int("n"))), []hff.Value{n, hff.List(), hff.List(obj(i(1)), obj(i(1)), obj(i(2)), obj(n))}, []string{`items:distinct:size=3`, `items:distinct:unique`, `items:find(v=>v.n>1).n=2`, `items:find(v=>v.n!)={"n":1}`}},
	} {
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", tc.element)))), engine.WithCapabilities(capabilities.Standard()))
				if err != nil {
					t.Fatal(err)
				}
				var opts []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				var script strings.Builder
				var expected []bool
				var labels []string
				for _, source := range tc.sources {
					p, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, p)
					if err != nil {
						t.Fatal(err)
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
					params, arguments := "", ""
					if len(types) > 0 {
						params = "(" + strings.Join(types, ",") + ")"
						arguments = "(" + strings.Join(args, ",") + ")"
					}
					for _, value := range tc.rows {
						matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": value}))
						if err != nil {
							t.Fatal(source, err)
						}
						name := fmt.Sprintf("selection_%d", len(expected))
						fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb)) records(items,metadata); EXECUTE %s%s; DEALLOCATE %s;\n", name, params, q.Where(), sqlFixtureString(metadataJSONFixture(t, value)), format, sqlFixtureString(pairTypeMetadata(t, value)), name, arguments, name)
						expected = append(expected, matched)
						labels = append(labels, source+" on "+metadataJSONFixture(t, value))
					}
				}
				command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				command.Stdin = strings.NewReader(script.String())
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("SQL list selection failed: %v\n%s", err, output)
				}
				results := strings.Fields(string(output))
				if len(results) != len(expected) {
					t.Fatalf("got %d results want %d: %s", len(results), len(expected), output)
				}
				for index, result := range results {
					if (result == "t") != expected[index] {
						t.Errorf("%s: SQL=%s Local=%v", labels[index], result, expected[index])
					}
				}
			})
		}
	}
}
