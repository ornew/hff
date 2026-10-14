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

func TestPostgreSQLCountAndFilterMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", schema.AnyType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []hff.Value{
		hff.Null(),
		hff.List(),
		hff.List(hff.Int(0), hff.Int(1), hff.Null(), hff.Int(2)),
		hff.List(hff.Null(), hff.Object(map[string]hff.Value{"active": hff.Bool(true)}), hff.Object(map[string]hff.Value{"active": hff.Bool(false)})),
		hff.List(hff.Int(0), hff.Float(math.Copysign(0, -1)), hff.Float(1)),
	}
	sources := []string{
		`items:count=0`,
		`items:count(v=>v)=2`,
		`items:count(v=>v!.active)=1`,
		`items:filter(v=>v):json="[1,2]"`,
		`items:filter(v=>v:[int]:exists):json="[0,1,2]"`,
		`items:filter(v=>v!.active):count=1`,
		`items:filter(v=>v:[float]:exists):json="[-0.0,1.0]"`,
		`items:map(v=>[v!]):filter(v=>true):count=3`,
		`items:map(v=>[v!]):filter(v=>true):json:contains("-0.0")`,
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			var script strings.Builder
			var expected, labels []string
			for _, source := range sources {
				program, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, program)
				if err != nil {
					t.Fatal(source, err)
				}
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
				}
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
					hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...)))
				if err != nil {
					t.Fatal(source, err)
				}
				var types, literals []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, literals = append(types, typ), append(literals, literal)
				}
				params, args := "", ""
				if len(types) > 0 {
					params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
				}
				for _, value := range rows {
					matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": value}))
					if err != nil {
						t.Fatal(source, err)
					}
					want := "f"
					if matched {
						want = "t"
					}
					expected = append(expected, want)
					labels = append(labels, source+" on "+metadataJSONFixture(t, value))
					name := fmt.Sprintf("count_filter_%d", len(expected))
					fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb)) records(items,meta); EXECUTE %s%s; DEALLOCATE %s;\n",
						name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, value)), format,
						sqlFixtureString(pairTypeMetadata(t, value)), name, args, name)
				}
			}
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script.String())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("PostgreSQL count/filter failed: %v\n%s", err, output)
			}
			actual := strings.Fields(string(output))
			if len(actual) != len(expected) {
				t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
			}
			for i, want := range expected {
				if actual[i] != want {
					t.Errorf("%s: PostgreSQL=%s local=%s", labels[i], actual[i], want)
				}
			}
		})
	}
}
