//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

func TestPostgreSQLDynamicJSONPairs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("left"), schema.Any("right"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]hff.Value{
		{hff.Null(), hff.Null()}, {hff.Null(), hff.String("")},
		{hff.Int(9007199254740993), hff.Float(9007199254740992)},
		{hff.Float(9007199254740992), hff.Int(9007199254740993)},
		{hff.Int(1), hff.Float(1)}, {hff.Int(-9223372036854775808), hff.Float(-9223372036854775808)},
		{hff.Float(1e20), hff.Float(1e20)}, {hff.Float(5e-324), hff.Int(0)},
		{hff.Int(9223372036854775807), hff.Float(9223372036854775808)},
		{hff.Bool(true), hff.Bool(false)}, {hff.Bool(true), hff.Int(1)},
		{hff.String("é"), hff.String("z")}, {hff.String("1"), hff.Int(1)},
		{hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null())},
		{hff.List(hff.Int(1)), hff.List(hff.String("1"))},
		{hff.List(), hff.List(hff.Null())}, {hff.List(), hff.Object(nil)},
		{hff.List(hff.Int(1), hff.Float(2)), hff.Float(2)},
		{hff.Object(map[string]hff.Value{"": hff.Null()}), hff.Object(nil)},
		{hff.Object(map[string]hff.Value{"a/b~": hff.List(hff.Int(1), hff.Null())}), hff.Object(map[string]hff.Value{"a/b~": hff.List(hff.Float(1), hff.Null())})},
		{hff.Object(map[string]hff.Value{"x": hff.Bool(false)}), hff.Object(map[string]hff.Value{"x": hff.Int(0)})},
		{hff.List(hff.Int(9007199254740993)), hff.List(hff.Float(9007199254740992))},
		{hff.Object(map[string]hff.Value{"x": hff.Int(1), "y": hff.Null()}), hff.Object(map[string]hff.Value{"y": hff.Null(), "x": hff.Float(1)})},
	}
	deepInt, deepFloat := hff.Int(1), hff.Float(1)
	for range 32 {
		deepInt, deepFloat = hff.List(deepInt), hff.List(deepFloat)
	}
	pairs = append(pairs, [2]hff.Value{deepInt, deepFloat})
	sources := []string{`left=right`, `left!=right`, `left<right`, `left>=right`, `(left=right):type="null"`, `(left<right):type="null"`, `(left:[string]=right):type="bool"`, `(left:[int|string]=right):type="bool"`, `left=[1,null]`, `left!={"x":false}`, `(left=[1,null]):type="null"`, `left:[list[number]]=right`, `left=right:[list[number]]`, `left:[list[any]]:any(x=>x=right)`, `left:[list[any]]:any(x=>right=x)`}
	var script strings.Builder
	var expected []bool
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range sources {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(err)
			}
			var bindings []hffsql.Binding
			for _, name := range []string{"left", "right"} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
				}
				bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if err != nil {
				t.Fatal(source, err)
			}
			var types, literals []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				literals = append(literals, literal)
			}
			for _, pair := range pairs {
				match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": pair[0], "right": pair[1]}))
				if err != nil {
					t.Fatal(err)
				}
				index := len(expected)
				arguments, parameters := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(literals, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE pair_%d%s AS SELECT %s FROM (VALUES (%s::%s,%s::%s,%s::jsonb,%s::jsonb)) records(\"left\",\"right\",left_meta,right_meta); EXECUTE pair_%d%s;\n", index, parameters, query.Where(), sqlFixtureString(metadataJSONFixture(t, pair[0])), format, sqlFixtureString(metadataJSONFixture(t, pair[1])), format, sqlFixtureString(pairTypeMetadata(t, pair[0])), sqlFixtureString(pairTypeMetadata(t, pair[1])), index, arguments)
				expected = append(expected, match)
				descriptions = append(descriptions, source+" "+string(format)+" "+metadataJSONFixture(t, pair[0])+" / "+metadataJSONFixture(t, pair[1]))
			}
		}
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), output)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", descriptions[i], result, expected[i])
		}
	}
}

func pairTypeMetadata(t testing.TB, value hff.Value) string {
	t.Helper()
	types := map[string]map[string]string{}
	var walk func(hff.Value, string)
	walk = func(value hff.Value, pointer string) {
		switch value.Kind() {
		case hff.IntKind, hff.FloatKind:
			kind := "int"
			if value.Kind() == hff.FloatKind {
				kind = "float"
			}
			types[pointer] = map[string]string{"type": kind, "text": metadataJSONFixture(t, value)}
		case hff.ListKind:
			values, _ := value.AsList()
			for i, item := range values {
				walk(item, pointer+"/"+strconv.Itoa(i))
			}
		case hff.ObjectKind:
			values, _ := value.AsObject()
			for key, item := range values {
				walk(item, pointer+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
			}
		}
	}
	walk(value, "")
	data, err := json.Marshal(map[string]any{"version": 1, "types": types})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPostgreSQLSchemaKnownDynamicPairs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx := context.Background()
	floatObject := schema.Struct(schema.Optional(schema.Nullable(schema.Float("n"))))
	intObject := schema.Struct(schema.Optional(schema.Nullable(schema.Int("n"))))
	for _, tc := range []struct {
		leftType, rightType hff.Type
		left, right         hff.Value
	}{
		{schema.FloatType, schema.IntType, hff.Float(9007199254740992), hff.Int(9007199254740993)},
		{schema.FloatType, schema.IntType, hff.Float(1), hff.Int(1)},
		{schema.FloatType, schema.FloatType, hff.Float(1e20), hff.Float(1e20)},
		{schema.ListOf(schema.FloatType), schema.ListOf(schema.IntType), hff.List(hff.Float(9007199254740992)), hff.List(hff.Int(9007199254740993))},
		{schema.MapOf(schema.FloatType), schema.MapOf(schema.IntType), hff.Object(map[string]hff.Value{"n": hff.Float(1)}), hff.Object(map[string]hff.Value{"n": hff.Int(1)})},
		{floatObject, intObject, hff.Object(nil), hff.Object(map[string]hff.Value{"n": hff.Null()})},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", schema.Union(tc.leftType, schema.StringType)), schema.Field("right", schema.Union(tc.rightType, schema.StringType)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`left=right`, `left!=right`, `(left<right):type="null"`} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": tc.left, "right": tc.right}))
			if err != nil {
				t.Fatal(err)
			}
			query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "left"), hffsql.JSONB), hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "right"), hffsql.JSONB)))
			if err != nil {
				t.Fatal(source, err)
			}
			var types, literals []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				literals = append(literals, literal)
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(literals, ",") + ")"
			}
			script := fmt.Sprintf("PREPARE known%s AS SELECT %s FROM (VALUES (%s::jsonb,%s::jsonb)) records(\"left\",\"right\"); EXECUTE known%s;", parameters, query.Where(), sqlFixtureString(metadataJSONFixture(t, tc.left)), sqlFixtureString(metadataJSONFixture(t, tc.right)), arguments)
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script)
			output, err := command.CombinedOutput()
			if err != nil || (strings.TrimSpace(string(output)) == "t") != want {
				t.Fatalf("%s, %v/%v: want%v, got%s (%v)", source, tc.leftType, tc.rightType, want, output, err)
			}
		}
	}
}

func TestPostgreSQLExpectedActualJSONRecipe(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("event", schema.Struct(schema.Any("expected"), schema.Any("actual"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `event.expected=event.actual`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"event"}, hffsql.Column("checks", "event"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("checks", "event_types")))))
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	object := hff.Object(map[string]hff.Value{"count": hff.Int(9007199254740993), "labels": hff.List(hff.String("a"), hff.Null())})
	cases := [][2]hff.Value{
		{object, object},
		{hff.Object(map[string]hff.Value{"steps": hff.List(hff.Int(1), hff.Int(2))}), hff.Object(map[string]hff.Value{"steps": hff.List(hff.Int(2), hff.Int(1))})},
		{hff.Int(42), hff.String("42")},
		{hff.Int(1), hff.Float(1)},
	}
	var rows []string
	var expected []string
	for i, pair := range cases {
		event := hff.Object(map[string]hff.Value{"expected": pair[0], "actual": pair[1]})
		match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"event": event}))
		if err != nil {
			t.Fatal(err)
		}
		id := strconv.Itoa(101 + i)
		if match {
			expected = append(expected, id)
		}
		rows = append(rows, "("+id+","+sqlFixtureString(metadataJSONFixture(t, event))+"::jsonb,"+sqlFixtureString(pairTypeMetadata(t, event))+"::jsonb)")
	}
	if strings.Join(expected, ",") != "101,104" {
		t.Fatalf("recipe local IDs=%v", expected)
	}
	var types, literals []string
	for _, arg := range query.Args() {
		typ, literal := sqlFixtureParameter(t, arg)
		types = append(types, typ)
		literals = append(literals, literal)
	}
	script := fmt.Sprintf("PREPARE recipe(%s) AS SELECT checks.id FROM (VALUES %s) checks(id,event,event_types) WHERE %s ORDER BY checks.id; EXECUTE recipe(%s);", strings.Join(types, ","), strings.Join(rows, ","), query.Where(), strings.Join(literals, ","))
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil || strings.Join(strings.Fields(string(output)), ",") != strings.Join(expected, ",") {
		t.Fatalf("recipe expected IDs%v, got%s (%v)", expected, output, err)
	}
}
