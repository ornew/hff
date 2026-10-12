//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLJSONMetadataRestoresNumericText(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("data", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	sources := []string{`data["a/b~"]:tostring="-0.0"`, `data:tojson="{\"a/b~\":-0.0,\"日\":1.0}"`, `data:values:any(x=>x:tostring="-0.0")`, `data["日"]=1.0`, `data:[map[float]]["a/b~"]:tostring="-0.0"`}
	for index, source := range sources {
		activeEngine := e
		if strings.Contains(source, ":[map[float]]") {
			activeEngine, err = engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
		}
		program, err := activeEngine.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		query, err := hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))))
		if err != nil {
			t.Fatal(err)
		}
		var types, literals []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			literals = append(literals, value)
		}
		fmt.Fprintf(&script, "PREPARE metadata_%d(%s) AS SELECT %s FROM (VALUES ('{\"a/b~\":0,\"日\":1}'::jsonb,'{\"version\":1,\"types\":{\"/a~1b~0\":{\"type\":\"float\",\"text\":\"-0.0\"},\"/日\":{\"type\":\"float\"}}}'::jsonb)) records(data,meta); EXECUTE metadata_%d(%s);\n", index, strings.Join(types, ","), query.Where(), index, strings.Join(literals, ","))
	}
	command := exec.Command("docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(sources) {
		t.Fatalf("%s", output)
	}
	for index, result := range results {
		if result != "t" {
			t.Errorf("%s: %s", sources[index], result)
		}
	}
}

func TestPostgreSQLDynamicJSONGuards(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	supported := map[string]bool{}
	for _, name := range []string{"explicit_null_test", "type_guard_failure_is_not_null", "exists_observes_failed_type_guard", "guard_failure_is_not_null", "not_inverts_failed_guard", "exists_observes_falsy_values", "explicit_type_guard", "any_and_all_empty_lists", "callback_predicate_guard", "null_type_is_observable", "object_lookup_distinguishes_missing", "type_guard_limits_method_permissions"} {
		supported[name] = true
	}
	var script strings.Builder
	var expected []string
	for _, tc := range conformance.Cases() {
		if !supported[tc.Name] {
			continue
		}
		e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(context.Background(), tc.Source)
		if err != nil {
			t.Fatal(err)
		}
		var bindings []hffsql.Binding
		for _, field := range tc.Schema.Fields() {
			bindings = append(bindings, hffsql.BindJSON(hff.Path{field.Name}, hffsql.Column("records", field.Name), hffsql.JSON))
		}
		query, err := hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(tc.Name, err)
		}
		for _, row := range tc.Records {
			var columns, values, types, literals []string
			for _, field := range tc.Schema.Fields() {
				value, _ := row.Value.Lookup(field.Name)
				columns = append(columns, `"`+field.Name+`"`)
				values = append(values, sqlFixtureString(metadataJSONFixture(t, value))+"::json")
			}
			for _, arg := range query.Args() {
				typ, value := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				literals = append(literals, value)
			}
			params, args := "", ""
			if len(types) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				args = "(" + strings.Join(literals, ",") + ")"
			}
			index := len(expected)
			want := "f"
			if row.Match {
				want = "t"
			}
			expected = append(expected, want)
			fmt.Fprintf(&script, "PREPARE guard_%d%s AS SELECT %s FROM (VALUES (%s)) records(%s); EXECUTE guard_%d%s;\n", index, params, query.Where(), strings.Join(values, ","), strings.Join(columns, ","), index, args)
		}
	}
	command := exec.Command("docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(expected) {
		t.Fatalf("got %d want %d: %s", len(results), len(expected), output)
	}
	for index, result := range results {
		if result != expected[index] {
			t.Errorf("case%d: got%s want%s", index, result, expected[index])
		}
	}
}
func metadataJSONFixture(t *testing.T, value hff.Value) string {
	t.Helper()
	switch value.Kind() {
	case hff.NullKind:
		return "null"
	case hff.BoolKind:
		x, _ := value.AsBool()
		return strconv.FormatBool(x)
	case hff.IntKind:
		x, _ := value.AsInt()
		return strconv.FormatInt(x, 10)
	case hff.FloatKind:
		x, _ := value.AsFloat()
		out := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(out, ".eE") {
			out += ".0"
		}
		return out
	case hff.StringKind:
		x, _ := value.AsString()
		encoded, _ := json.Marshal(x)
		return string(encoded)
	case hff.ListKind:
		list, _ := value.AsList()
		var values []string
		for _, x := range list {
			values = append(values, metadataJSONFixture(t, x))
		}
		return "[" + strings.Join(values, ",") + "]"
	case hff.ObjectKind:
		object, _ := value.AsObject()
		var keys []string
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var values []string
		for _, key := range keys {
			encoded, _ := json.Marshal(key)
			values = append(values, string(encoded)+":"+metadataJSONFixture(t, object[key]))
		}
		return "{" + strings.Join(values, ",") + "}"
	default:
		t.Fatalf("unsupported fixture %v", value.Kind())
		return ""
	}
}

func TestPostgreSQLJSONMetadataNestedBindings(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	payloadType := schema.MapOf(schema.FloatType)
	itemType := schema.Struct(schema.Field("payload", payloadType))
	correlation := hffsql.Equal(
		hffsql.ColumnValue(hffsql.Column("child", "parent_id"), schema.IntType),
		hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType),
	)
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("metadata_children"), "child"),
		hffsql.WithCorrelation(correlation), hffsql.WithRelationItem(itemType,
			hffsql.BindStruct(hff.Path{}, hffsql.Boolean(true), hffsql.Boolean(false),
				hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("child", "data"), hffsql.JSONB,
					hffsql.WithTypeMetadata(hffsql.Column("child", "meta"))))),
		hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("child", "id"), schema.IntType)),
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, source, from string
		schema             hff.Type
		bindings           []hffsql.Binding
	}{
		{"flattened", `profile.payload["a/b~"]:tostring="-0.0"`, "metadata_parents records",
			schema.Struct(schema.Field("profile", itemType)),
			[]hffsql.Binding{hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false),
				hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "data"), hffsql.JSONB,
					hffsql.WithTypeMetadata(hffsql.Column("records", "meta"))))}},
		{"relation_predicate", `items:any(x=>x.payload["a/b~"]:tostring="-0.0")`, "metadata_parents records",
			schema.Struct(schema.List("items", itemType)), []hffsql.Binding{hffsql.BindRelation(hff.Path{"items"}, relation)}},
		{"relation_index", `items[0].payload:tojson="{\"a/b~\":-0.0,\"日\":1.0}"`, "metadata_parents records",
			schema.Struct(schema.List("items", itemType)), []hffsql.Binding{hffsql.BindRelation(hff.Path{"items"}, relation)}},
		{"nested_list", `data:[list[list[float]]][0][0]:tostring="-0.0"`, "metadata_arrays records",
			schema.Struct(schema.Any("data")), []hffsql.Binding{hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB,
				hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))}},
		{"scalar_root", `data:tostring="-0.0"`, "metadata_scalars records",
			schema.Struct(schema.Float("data")), []hffsql.Binding{hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB,
				hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))}},
		{"empty_key", `data:[map[float]][""]:tostring="-0.0"`, "metadata_empty_keys records",
			schema.Struct(schema.Any("data")), []hffsql.Binding{hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB,
				hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))}},
	}
	var script strings.Builder
	script.WriteString(`CREATE TEMP TABLE metadata_parents(id bigint, data jsonb, meta jsonb);
INSERT INTO metadata_parents VALUES (42,'{"a/b~":0,"日":1}',
'{"version":1,"types":{"/a~1b~0":{"type":"float","text":"-0.0"},"/日":{"type":"float"}}}');
CREATE TEMP TABLE metadata_children(id bigint,parent_id bigint,data jsonb,meta jsonb);
INSERT INTO metadata_children SELECT 7,id,data,meta FROM metadata_parents;
CREATE TEMP TABLE metadata_arrays(data jsonb,meta jsonb);
INSERT INTO metadata_arrays VALUES ('[[0,1],[2]]',
'{"version":1,"types":{"/0/0":{"type":"float","text":"-0.0"},"/0/1":{"type":"float"},"/1/0":{"type":"float"}}}');
CREATE TEMP TABLE metadata_scalars(data jsonb,meta jsonb);
INSERT INTO metadata_scalars VALUES ('0',
'{"version":1,"types":{"":{"type":"float","text":"-0.0"}}}');
CREATE TEMP TABLE metadata_empty_keys(data jsonb,meta jsonb);
INSERT INTO metadata_empty_keys VALUES ('{"":0}',
'{"version":1,"types":{"/":{"type":"float","text":"-0.0"}}}');
`)
	for index, tc := range cases {
		e, err := engine.New(engine.WithSchema(tc.schema), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(tc.name, err)
		}
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(tc.bindings...))
		if err != nil {
			t.Fatal(tc.name, err)
		}
		var types, literals []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			literals = append(literals, value)
		}
		fmt.Fprintf(&script, "PREPARE nested_metadata_%d(%s) AS SELECT %s FROM %s; EXECUTE nested_metadata_%d(%s);\n", index, strings.Join(types, ","), query.Where(), tc.from, index, strings.Join(literals, ","))
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
	for index, result := range results {
		if result != "t" {
			t.Errorf("%s: got %s", cases[index].name, result)
		}
	}
}

func TestPostgreSQLJSONMetadataAliasScopes(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	script.WriteString(`CREATE TEMP TABLE metadata_scope_records(id bigint);
INSERT INTO metadata_scope_records VALUES(42);
CREATE TEMP TABLE metadata_scope_settings(record_id bigint,data jsonb,meta jsonb);
INSERT INTO metadata_scope_settings VALUES(42,'{"threshold":7}', '{"version":1,"types":{}}');
CREATE TEMP TABLE metadata_scope_children(record_id bigint,value bigint);
INSERT INTO metadata_scope_children VALUES(42,7);
`)
	cases := []struct {
		source, from string
		schema       hff.Type
		bindings     []hffsql.Binding
	}{}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("metadata_scope_children"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "record_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType))),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(hff.Path{}, hffsql.Column("item", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		source, from string
		schema       hff.Type
		bindings     []hffsql.Binding
	}{
		`items:any(x=>x=data["threshold"])`, `metadata_scope_records records JOIN metadata_scope_settings item ON item.record_id=records.id`,
		schema.Struct(schema.Map("data", schema.IntType), schema.List("items", schema.IntType)),
		[]hffsql.Binding{
			hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("item", "data"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("item", "meta"))),
			hffsql.BindRelation(hff.Path{"items"}, relation),
		},
	})
	// A caller alias identical to the first generated CTE name must be reserved.
	cases = append(cases, struct {
		source, from string
		schema       hff.Type
		bindings     []hffsql.Binding
	}{
		`data["threshold"]=7`, `metadata_scope_settings "__hff_1"`,
		schema.Struct(schema.Map("data", schema.IntType)),
		[]hffsql.Binding{hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("__hff_1", "data"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("__hff_1", "meta")))},
	})
	// Reserve later bindings too, before the first binding restores metadata.
	cases = append(cases, struct {
		source, from string
		schema       hff.Type
		bindings     []hffsql.Binding
	}{
		`data["threshold"]=other`, `metadata_scope_settings settings JOIN metadata_scope_children "__hff_1" ON "__hff_1".record_id=settings.record_id`,
		schema.Struct(schema.Map("data", schema.IntType), schema.Int("other")),
		[]hffsql.Binding{
			hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("settings", "data"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("settings", "meta"))),
			hffsql.Bind(hff.Path{"other"}, hffsql.Column("__hff_1", "value")),
		},
	})
	for index, tc := range cases {
		e, err := engine.New(engine.WithSchema(tc.schema), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(tc.bindings...))
		if err != nil {
			t.Fatal(err)
		}
		var types, literals []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			literals = append(literals, value)
		}
		params, args := "", ""
		if len(types) > 0 {
			params = "(" + strings.Join(types, ",") + ")"
			args = "(" + strings.Join(literals, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE metadata_scope_%d%s AS SELECT %s FROM %s; EXECUTE metadata_scope_%d%s;\n", index, params, query.Where(), tc.from, index, args)
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
	for index, result := range results {
		if result != "t" {
			t.Errorf("case %d: %s", index, result)
		}
	}
}

func TestPostgreSQLDynamicJSONIndexing(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		source, document string
		match            bool
	}{
		{`data["key"]:exists`, `{"key":false}`, true},
		{`data["key"]:exists`, `{"key":null}`, false},
		{`data["key"]:exists`, `[]`, false},
		{`data[0]:type="null"`, `{"key":false}`, true},
		{`data[-1]:exists`, `[0]`, true},
		{`data[1.5]:exists`, `[0,1]`, false},
		{`data[0]:exists`, `"hello"`, false},
		{`data[0]:exists`, `null`, false},
		{`data[1]:exists`, `[0]`, false},
	}
	var script strings.Builder
	for index, tc := range cases {
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSON)))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		var types, literals []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			literals = append(literals, value)
		}
		fmt.Fprintf(&script, "PREPARE dynamic_index_%d(%s) AS SELECT %s FROM (VALUES (%s::json)) records(data); EXECUTE dynamic_index_%d(%s);\n", index, strings.Join(types, ","), query.Where(), sqlFixtureString(tc.document), index, strings.Join(literals, ","))
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
			t.Errorf("%s on %s: %s", cases[i].source, cases[i].document, result)
		}
	}
}
