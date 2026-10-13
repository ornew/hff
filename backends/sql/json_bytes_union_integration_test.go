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

func TestPostgreSQLJSONBytesScalarUnionMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	typ := schema.Union(schema.BytesType, schema.BoolType, schema.IntType, schema.FloatType, schema.NullType)
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", typ), schema.List("items", typ),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value     hff.Value
		json      string
		meta      string
		items     []hff.Value
		itemsJSON string
		itemsMeta string
	}{
		{value: hff.Bytes(nil), json: `""`, meta: `{"version":1,"types":{}}`},
		{value: hff.Bytes([]byte("a")), json: `"YQ=="`, meta: `{"version":1,"types":{}}`},
		{value: hff.Bytes([]byte{0}), json: `"AA=="`, meta: `{"version":1,"types":{}}`},
		{value: hff.Bool(true), json: `true`, meta: `{"version":1,"types":{}}`},
		{value: hff.Bool(false), json: `false`, meta: `{"version":1,"types":{}}`},
		{value: hff.Int(0), json: `0`, meta: `{"version":1,"types":{"":{"type":"int"}}}`},
		{value: hff.Int(9007199254740993), json: `9007199254740993`, meta: `{"version":1,"types":{"":{"type":"int"}}}`,
			items:     []hff.Value{hff.Int(9007199254740993), hff.Float(9007199254740992)},
			itemsJSON: `[9007199254740993,9007199254740992.0]`,
			itemsMeta: `{"version":1,"types":{"/0":{"type":"int"},"/1":{"type":"float","text":"9007199254740992.0"}}}`},
		{value: hff.Float(9007199254740992), json: `9007199254740992.0`, meta: `{"version":1,"types":{"":{"type":"float","text":"9007199254740992.0"}}}`},
		{value: hff.Float(math.Copysign(0, -1)), json: `-0.0`, meta: `{"version":1,"types":{"":{"type":"float","text":"-0.0"}}}`,
			items:     []hff.Value{hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0)},
			itemsJSON: `[-0.0,0,0.0]`,
			itemsMeta: `{"version":1,"types":{"/0":{"type":"float","text":"-0.0"},"/1":{"type":"int"},"/2":{"type":"float","text":"0.0"}}}`},
		{value: hff.Null(), json: `null`, meta: `{"version":1,"types":{}}`},
	}
	sources := []string{
		`value:type="bytes"`, `value:type="bool"`, `value:len=1`, `value:[bytes]:len=1`, `value:[int]>0`,
		`value="YQ==":frombase64`, `value!=value`, `value=value`, `value=1`, `value=true`, `value>0`,
		`value:in("YQ==":frombase64,1)`, `value:string="YQ=="`,
		`value:json="\"YQ==\""`, `items:contains("YQ==":frombase64)`,
		`items=items`, `items:unique`, `items:sort=null`, `items:sort:json="[9007199254740992.0,9007199254740993]"`,
		`items:sort:json="[-0.0,0,0.0]"`, `items:map(x=>x:type):contains("bytes")`,
	}
	for _, mode := range []struct {
		name   string
		format hffsql.JSONFormat
		meta   bool
	}{{"json", hffsql.JSON, false}, {"jsonb-metadata", hffsql.JSONB, true}} {
		t.Run(mode.name, func(t *testing.T) {
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
				options := []hffsql.JSONBindingOption{}
				itemOptions := []hffsql.JSONBindingOption{}
				if mode.meta {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
					itemOptions = append(itemOptions, hffsql.WithTypeMetadata(hffsql.Column("records", "itemmeta")))
				}
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), mode.format, options...),
					hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), mode.format, itemOptions...),
				))
				if err != nil {
					t.Fatal(source, err)
				}
				var parameterTypes, arguments []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					parameterTypes = append(parameterTypes, typ)
					arguments = append(arguments, literal)
				}
				var values []string
				for ri, row := range rows {
					items := hff.List(row.value, hff.Bytes([]byte("a")))
					if row.items != nil {
						items = hff.List(row.items...)
					}
					match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"value": row.value, "items": items}))
					if err != nil {
						t.Fatal(source, ri, err)
					}
					expected = append(expected, match)
					labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
					itemsJSON := "[" + row.json + `,"YQ=="]`
					itemsMeta := strings.Replace(row.meta, `"":`, `"/0":`, 1)
					if row.itemsJSON != "" {
						itemsJSON, itemsMeta = row.itemsJSON, row.itemsMeta
					}
					values = append(values, "("+sqlFixtureString(row.json)+"::"+string(mode.format)+","+sqlFixtureString(row.meta)+"::jsonb,"+
						sqlFixtureString(itemsJSON)+"::"+string(mode.format)+","+sqlFixtureString(itemsMeta)+"::jsonb)")
				}
				params, args := "", ""
				if len(parameterTypes) > 0 {
					params = "(" + strings.Join(parameterTypes, ",") + ")"
					args = "(" + strings.Join(arguments, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE json_bytes_union_%d%s AS SELECT %s FROM (VALUES %s) records(value,meta,items,itemmeta); EXECUTE json_bytes_union_%d%s;\n", si, params, query.Where(), strings.Join(values, ","), si, args)
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
					t.Errorf("%s: PostgreSQL=%s local=%v", labels[i], result, expected[i])
				}
			}
		})
	}
}
