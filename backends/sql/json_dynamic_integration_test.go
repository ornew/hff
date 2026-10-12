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
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLDynamicJSONScalarComparisonStates(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"), schema.Int("known"), schema.String("name"), schema.Bool("flag"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		source, document string
		match            bool
	}{
		{`known>data`, `9007199254740992.0`, true},
		{`known=data`, `9007199254740992.0`, false},
		{`known<data`, `9007199254740994.0`, true},
		{`known=data`, `"9007199254740993"`, false},
		{`data:not`, `false`, true},
		{`(data:not):type="null"`, `42`, true},
		{`data:or(true)`, `false`, true},
		{`data:and(true)`, `true`, true},
		{`(data:or(data:[bool])):type="null"`, `42`, true},
		{`(flag:and(data:[bool])):type="bool"`, `"wrong"`, true},
		{`(flag:or(data:[bool])):type="null"`, `"wrong"`, false},
		{`(flag:or(data)):type="null"`, `42`, true},
		{`name:startswith(data)`, `"Al"`, true},
		{`name:endswith(data)`, `"ice"`, true},
		{`(name:startswith(data)):type="null"`, `42`, true},
		{`data:len=2`, `"éa"`, true},
		{`(data:len):type="null"`, `42`, true},
		{`data:startswith("A")`, `"Alice"`, true},
		{`data:endswith("e")`, `"Alice"`, true},
		{`data:startswith(data)`, `"Alice"`, true},
		{`(data:startswith(data:[string])):type="null"`, `true`, true},
		{`(data:[string|int]:len):type="null"`, `true`, false},
		{`data="Alice"`, `"Alice"`, true},
		{`data="Alice"`, `"Bob"`, false},
		{`data!="Alice"`, `42`, false},
		{`(data="Alice"):type="null"`, `42`, true},
		{`(data="Alice"):exists`, `42`, false},
		{`(data="Alice"):type="bool"`, `null`, true},
		{`(data="Alice"):exists`, `null`, true},
		{`!(data="Alice")`, `42`, true},
		{`(data="Alice"):not`, `42`, false},
		{`data<"Bob"`, `"Alice"`, true},
		{`data<"Bob"`, `[]`, false},
		{`data=true`, `true`, true},
		{`data!=true`, `false`, true},
		{`(data=true):type="null"`, `"true"`, true},
		{`data=1`, `1.0`, true},
		{`data=0`, `-0.0`, true},
		{`(data=1):type="null"`, `{}`, true},
		{`(data:[int|string]=1):type="bool"`, `true`, true},
		{`data>9007199254740992.0`, `9007199254740993`, true},
		{`data=9007199254740993`, `9007199254740992.0`, false},
		{`data>1000000000000000100`, `1.0000000000000001e18`, true},
		{`data=1.0000000000000001e18`, `1000000000000000100`, false},
		{`(data>1.5):type="null"`, `"2"`, true},
		{`(data>1.5):type="bool"`, `null`, true},
		{`(data:[string]=null):type="bool"`, `null`, true},
		{`(data:[string]!=null):type="bool"`, `null`, true},
		{`(data:[string]!=null):exists`, `null`, true},
		{`!(data:[string]!=null)`, `null`, true},
		{`(data:[string]=null):exists`, `null`, true},
		{`data:[string]=null`, `null`, false},
	}
	var script strings.Builder
	for mode := 0; mode < 2; mode++ {
		for i, tc := range cases {
			format := hffsql.JSON
			metadata := `{"version":1,"types":{}}`
			var options []hffsql.JSONBindingOption
			if mode == 1 {
				format = hffsql.JSONB
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				if tc.document[0] == '-' || (tc.document[0] >= '0' && tc.document[0] <= '9') {
					kind := "int"
					if strings.ContainsAny(tc.document, ".eE") {
						kind = "float"
					}
					metadata = fmt.Sprintf(`{"version":1,"types":{"":{"type":%q,"text":%q}}}`, kind, tc.document)
				}
			}
			program, err := e.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...), hffsql.Bind(hff.Path{"known"}, hffsql.Column("records", "known")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")), hffsql.Bind(hff.Path{"flag"}, hffsql.Column("records", "flag"))))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			var types, args []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, literal)
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			index := mode*len(cases) + i
			fmt.Fprintf(&script, "PREPARE dynamic_compare_%d%s AS SELECT %s FROM (VALUES (%s::%s, 9007199254740993::bigint, %s::jsonb, 'Alice'::text, FALSE::boolean)) records(data,known,metadata,name,flag); EXECUTE dynamic_compare_%d%s;\n", index, parameters, query.Where(), sqlFixtureString(tc.document), format, sqlFixtureString(metadata), index, arguments)
		}
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != 2*len(cases) {
		t.Fatalf("results: %s", output)
	}
	for i, result := range results {
		if (result == "t") != cases[i%len(cases)].match {
			t.Errorf("%s on %s: %s", cases[i%len(cases)].source, cases[i%len(cases)].document, result)
		}
	}
}

func TestPostgreSQLUnionCollectionGuardsPreserveSchema(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	floatOrNull := schema.Union(schema.FloatType, schema.NullType)
	cases := []struct {
		typ              hff.Type
		source, document string
		format           hffsql.JSONFormat
		match            bool
	}{
		{schema.IntType, `data=1`, `1.0`, hffsql.JSONB, true},
		{schema.IntType, `data=9223372036854775807`, `9.223372036854775807e18`, hffsql.JSON, true},
		{schema.IntType, `data=-9223372036854775808`, `-9223372036854775808.0`, hffsql.JSONB, true},
		{schema.IntType, `data=1`, `1e0`, hffsql.JSON, true},
		{schema.Union(schema.IntType, schema.StringType), `data=1`, `1.0`, hffsql.JSONB, true},
		{schema.Union(schema.IntType, schema.StringType), `data:[int]=1`, `1e0`, hffsql.JSON, true},
		{schema.ListOf(schema.IntType), `data=[2,1]`, `[2.0,1e0]`, hffsql.JSONB, true},
		{schema.ListOf(schema.IntType), `data:sort=[1,2]`, `[2.0,1e0]`, hffsql.JSONB, true},
		{schema.ListOf(schema.IntType), `data:tojson="[2,1]"`, `[2.0,1e0]`, hffsql.JSONB, true},
		{schema.Union(schema.ListOf(schema.FloatType), schema.ListOf(floatOrNull), schema.StringType), `data:[list[float]]:exists`, `[1]`, hffsql.JSONB, true},
		{schema.Union(schema.ListOf(schema.FloatType), schema.ListOf(floatOrNull), schema.StringType), `data:[list[float]]:exists`, `"hello"`, hffsql.JSON, false},
		{schema.Union(schema.ListOf(schema.FloatType), schema.ListOf(floatOrNull)), `data:[list[float]]:exists`, `[null]`, hffsql.JSONB, false},
		{schema.Union(schema.ListOf(schema.IntType), schema.ListOf(schema.StringType)), `data:[list[int]]:exists`, `["hello"]`, hffsql.JSON, false},
		{schema.Union(schema.MapOf(schema.FloatType), schema.MapOf(floatOrNull)), `data:[map[float]]:exists`, `{"x":1}`, hffsql.JSONB, true},
		{schema.Union(schema.MapOf(schema.FloatType), schema.MapOf(floatOrNull)), `data:[map[float]]:exists`, `{"x":null}`, hffsql.JSONB, false},
		{schema.Union(schema.Struct(schema.Float("price")), schema.Struct(schema.Nullable(schema.Float("price")))), `data:[map[float]]:exists`, `{"price":1}`, hffsql.JSONB, true},
		{schema.Union(schema.AnyType, schema.ListOf(schema.FloatType), schema.ListOf(floatOrNull)), `data:[list[float]]:exists`, `[1]`, hffsql.JSON, false},
	}
	var script strings.Builder
	for i, tc := range cases {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), tc.format)))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE union_guard_%d%s AS SELECT %s FROM (VALUES (%s::%s)) records(data); EXECUTE union_guard_%d%s;\n", i, parameters, query.Where(), sqlFixtureString(tc.document), tc.format, i, arguments)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(cases) {
		t.Fatalf("results: %s", output)
	}
	for i, result := range results {
		if (result == "t") != cases[i].match {
			t.Errorf("case %d: %s on %s: %s", i, cases[i].source, cases[i].document, result)
		}
	}
}
