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

func TestPostgreSQLNullableAnyStringsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Any("value")),
		schema.Nullable(schema.Map("payload", schema.AnyType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	type record struct{ value, payload hff.Value }
	var records []record
	for _, value := range []hff.Value{
		hff.String("a"), hff.String("b"), hff.String(""), hff.Null(),
		hff.Int(1), hff.Float(1), hff.Bool(false), hff.List(), hff.Object(nil),
	} {
		records = append(records, record{value, hff.Object(map[string]hff.Value{"field": value})})
	}
	records = append(records, record{hff.Null(), hff.Object(nil)}, record{hff.Null(), hff.Null()})
	var sources []string
	for _, path := range []string{"value", "payload.field"} {
		sources = append(sources,
			path+`="a"`, path+`!="a"`, path+`<"b"`, `!(`+path+`="a")`,
			`(`+path+`="a"):type="null"`, path+`:type="string"`,
			path+`:[string]="a"`, path+`:[string]:exists`, path+`:len=1`,
			path+`=null`, path+`!:string="a"`,
		)
	}
	for _, mode := range []struct {
		name     string
		format   hffsql.JSONFormat
		metadata bool
	}{{"json", hffsql.JSON, false}, {"jsonb-metadata", hffsql.JSONB, true}} {
		t.Run(mode.name, func(t *testing.T) {
			var bindings []hffsql.Binding
			for _, name := range []string{"value", "payload"} {
				var options []hffsql.JSONBindingOption
				if mode.metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
				}
				bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), mode.format, options...))
			}
			var script strings.Builder
			var expected []bool
			var labels []string
			for si, source := range sources {
				program, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, program)
				if err != nil {
					t.Fatal(source, err)
				}
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
				if err != nil {
					t.Fatal(source, err)
				}
				var types, args []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, args = append(types, typ), append(args, literal)
				}
				var rows []string
				for ri, record := range records {
					want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": record.value, "payload": record.payload}))
					if err != nil {
						t.Fatal(source, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s row %d", source, ri))
					rows = append(rows, "("+sqlFixtureString(metadataJSONFixture(t, record.value))+"::"+string(mode.format)+","+
						sqlFixtureString(pairTypeMetadata(t, record.value))+"::jsonb,"+
						sqlFixtureString(metadataJSONFixture(t, record.payload))+"::"+string(mode.format)+","+
						sqlFixtureString(pairTypeMetadata(t, record.payload))+"::jsonb)")
				}
				parameters, arguments := "", ""
				if len(types) != 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE nullable_any_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(value,value_meta,payload,payload_meta); EXECUTE nullable_any_%d%s;\n", si, parameters, query.Where(), strings.Join(rows, ","), si, arguments)
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
				if result == "n" || (result == "true") != expected[i] {
					t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
				}
			}
		})
	}
}
