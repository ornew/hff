package config_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/config"
	"github.com/ornew/hff/engine"
)

func basicConfig() (engine.Config, sql.Config) {
	fields := hff.NewStructType(
		hff.Field{Name: "age", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "name", Type: hff.PrimitiveType(hff.StringType)},
	)
	return engine.Config{Schema: fields, Capabilities: capabilities.Standard()}, sql.Config{
		Dialect: sql.PostgreSQL,
		Bindings: []sql.Binding{
			sql.Bind(hff.Path{"age"}, sql.Column("people", "age")),
			sql.Bind(hff.Path{"name"}, sql.Column("people", "name")),
		},
	}
}

func TestSimpleConfigurationKeepsSQLQuery(t *testing.T) {
	e, backend := basicConfig()
	data, err := config.Encode(e, backend, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Decode(data, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Engine.Schema.Equal(e.Schema) || !reflect.DeepEqual(loaded.Engine.Capabilities.IDs(), e.Capabilities.IDs()) {
		t.Fatal("engine contract changed during round trip")
	}
	engineValue, err := engine.New(engine.WithConfig(&loaded.Engine))
	if err != nil {
		t.Fatal(err)
	}
	program, err := engineValue.Compile(context.Background(), `age>18 name="Alice"`)
	if err != nil {
		t.Fatal(err)
	}
	original, err := sql.Build(context.Background(), program, sql.WithConfig(&backend))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Build(context.Background(), program, sql.WithConfig(&loaded.SQL))
	if err != nil {
		t.Fatal(err)
	}
	if original.Where() != restored.Where() || !reflect.DeepEqual(original.Args(), restored.Args()) {
		t.Fatalf("SQL changed after round trip\noriginal %s %#v\nrestored %s %#v", original.Where(), original.Args(), restored.Where(), restored.Args())
	}
}

func TestBindingFamiliesRoundTrip(t *testing.T) {
	itemType := hff.NewStructType(hff.Field{Name: "price", Type: hff.PrimitiveType(hff.IntType)})
	item := sql.BindStruct(nil, sql.Boolean(true), sql.Boolean(false),
		sql.Bind(hff.Path{"price"}, sql.Column("item", "price")))
	relation, err := sql.NewRelation(
		sql.WithRelationSource(sql.Column("public", "items"), "item"),
		sql.WithCorrelation(sql.Equal(
			sql.ColumnValue(sql.Column("item", "person_id"), hff.PrimitiveType(hff.IntType)),
			sql.ColumnValue(sql.Column("person", "id"), hff.PrimitiveType(hff.IntType)))),
		sql.WithRelationItem(itemType, item),
		sql.WithUniqueOrder(sql.Ascending(sql.Column("item", "id"), hff.PrimitiveType(hff.IntType))),
	)
	if err != nil {
		t.Fatal(err)
	}
	bindingSet := []sql.Binding{
		sql.Bind(hff.Path{"id"}, sql.Column("person", "id")),
		sql.BindJSON(hff.Path{"data"}, sql.Column("person", "data"), sql.JSONB, sql.WithTypeMetadata(sql.Column("person", "data_types"))),
		sql.BindComposite(hff.Path{"address"}, sql.Column("person", "address")),
		sql.BindTimestamp(hff.Path{"created"}, sql.Column("person", "created"), sql.WithNanosecondRemainder(sql.Column("person", "created_ns"))),
		sql.BindStruct(hff.Path{"profile"}, sql.IsNotNull(sql.ColumnValue(sql.Column("person", "profile_id"), hff.PrimitiveType(hff.IntType))), sql.Boolean(false),
			sql.Bind(hff.Path{"score"}, sql.Column("person", "score"))),
		sql.BindRelation(hff.Path{"items"}, relation),
	}
	e := engine.Config{Schema: hff.NewStructType(
		hff.Field{Name: "id", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "data", Type: hff.NewMapType(hff.PrimitiveType(hff.AnyType))},
		hff.Field{Name: "address", Type: hff.NewStructType(hff.Field{Name: "city", Type: hff.PrimitiveType(hff.StringType)})},
		hff.Field{Name: "created", Type: hff.PrimitiveType(hff.TimestampType)},
		hff.Field{Name: "profile", Type: hff.NewStructType(hff.Field{Name: "score", Type: hff.PrimitiveType(hff.IntType)})},
		hff.Field{Name: "items", Type: hff.NewListType(itemType)},
	), Capabilities: capabilities.Standard()}
	data, err := config.Encode(e, sql.Config{Dialect: sql.PostgreSQL, Bindings: bindingSet}, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Decode(data, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SQL.Bindings) != len(bindingSet) {
		t.Fatal("lost bindings")
	}
	for i := range bindingSet {
		if !reflect.DeepEqual(bindingSet[i].View(), loaded.SQL.Bindings[i].View()) {
			t.Fatalf("binding %d changed during round trip\n%#v\n%#v", i, bindingSet[i].View(), loaded.SQL.Bindings[i].View())
		}
	}
}

func TestSchemaTypesAndMethodDeclarationRoundTrip(t *testing.T) {
	kinds := []hff.TypeKind{hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType,
		hff.BytesType, hff.TimestampType, hff.DurationType, hff.AnyType, hff.NeverType}
	var fields []hff.Field
	for _, kind := range kinds {
		fields = append(fields, hff.Field{Name: string(rune('a' + len(fields))), Type: hff.PrimitiveType(kind), Optional: true})
	}
	fields = append(fields,
		hff.Field{Name: "list", Type: hff.NewListType(hff.PrimitiveType(hff.StringType))},
		hff.Field{Name: "map", Type: hff.NewMapType(hff.PrimitiveType(hff.FloatType))},
		hff.Field{Name: "union", Type: hff.NewUnionType(hff.PrimitiveType(hff.NullType), hff.PrimitiveType(hff.StringType))},
	)
	e := engine.Config{Schema: hff.NewStructType(fields...), Capabilities: capabilities.None()}
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "example.combine.v1", Name: "combine",
		Receiver: hff.PrimitiveType(hff.StringType), Parameters: []hff.Type{hff.PrimitiveType(hff.IntType)},
		Optional: []hff.Type{hff.PrimitiveType(hff.BoolType)}, Variadic: hff.PrimitiveType(hff.StringType),
		Result: hff.PrimitiveType(hff.StringType), NullPolicy: hff.InvokeNull, BaseCost: 3})
	if err != nil {
		t.Fatal(err)
	}
	e.Methods = []hff.MethodDeclaration{declaration}
	e.Capabilities = e.Capabilities.Allow(capabilities.Method(declaration.ID()))
	data, err := config.Encode(e, sql.Config{Dialect: sql.PostgreSQL}, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Decode(data, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Engine.Schema.Equal(e.Schema) || len(loaded.Engine.Methods) != 1 || !loaded.Engine.Methods[0].Equal(declaration) {
		t.Fatal("schema or method declaration changed")
	}
}

func TestTrustedSQLRequiresRegisteredID(t *testing.T) {
	e, backend := basicConfig()
	e.Schema = hff.NewStructType(hff.Field{Name: "profile", Type: hff.NewStructType(hff.Field{Name: "score", Type: hff.PrimitiveType(hff.IntType)})})
	predicate := sql.TrustedSQL(`person.profile_present`, sql.Column("person", "profile_present"))
	backend.Bindings = []sql.Binding{sql.BindStruct(hff.Path{"profile"}, predicate, sql.Boolean(false),
		sql.Bind(hff.Path{"score"}, sql.Column("person", "score")))}
	if _, err := config.Encode(e, backend, config.Registry{}); err == nil {
		t.Fatal("unnamed trusted SQL was serialized")
	}
	registry := config.Registry{Predicates: map[string]sql.SQLPredicate{"profile-present-v1": predicate}}
	data, err := config.Encode(e, backend, registry)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("profile_present")) {
		t.Fatal("trusted SQL text leaked into JSON")
	}
	if !bytes.Contains(data, []byte(`"id": "profile-present-v1"`)) {
		t.Fatal("registry ID missing")
	}
	if _, err := config.Decode(data, config.Registry{}); err == nil {
		t.Fatal("missing registry ID was accepted")
	}
	if _, err := config.Decode(data, registry); err != nil {
		t.Fatal(err)
	}
}

func TestTranslatorRequiresRegisteredMatchingDeclaration(t *testing.T) {
	e, backend := basicConfig()
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
		ID: "service.score.v1", Name: "score", Receiver: hff.PrimitiveType(hff.IntType),
		Result: hff.PrimitiveType(hff.IntType), BaseCost: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Methods = []hff.MethodDeclaration{declaration}
	e.Capabilities = e.Capabilities.Allow(capabilities.Method(declaration.ID()))
	implementation := sql.Implement(declaration, sql.PostgreSQL, func(*sql.Call, sql.Expression, []sql.Expression) (sql.Expression, error) {
		return sql.Expression{}, nil
	})
	backend.Methods = []sql.MethodImplementation{implementation}
	registry := config.Registry{Methods: map[string]sql.MethodImplementation{declaration.ID(): implementation}}
	data, err := config.Encode(e, backend, registry)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("return sql.Expression")) {
		t.Fatal("translator source leaked")
	}
	if _, err := config.Decode(data, config.Registry{}); err == nil {
		t.Fatal("unregistered translator was accepted")
	}
	loaded, err := config.Decode(data, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SQL.Methods) != 1 || !loaded.SQL.Methods[0].Declaration().Equal(declaration) {
		t.Fatal("translator declaration changed")
	}
	separate := sql.Implement(declaration, sql.PostgreSQL, func(*sql.Call, sql.Expression, []sql.Expression) (sql.Expression, error) {
		return sql.Expression{}, nil
	})
	if _, err := config.Encode(e, backend, config.Registry{Methods: map[string]sql.MethodImplementation{declaration.ID(): separate}}); err == nil {
		t.Fatal("different implementation with same declaration was accepted")
	}
	backend.Methods[0] = sql.Implement(declaration, sql.PostgreSQL, nil)
	if _, err := config.Encode(e, backend, config.Registry{Methods: map[string]sql.MethodImplementation{declaration.ID(): backend.Methods[0]}}); err == nil {
		t.Fatal("nil translator function was accepted")
	}
}

func TestConfigurationRejectsAmbiguousOrUnknownInput(t *testing.T) {
	e, backend := basicConfig()
	base, err := config.Encode(e, backend, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"version":                bytes.Replace(base, []byte(`"version": 1`), []byte(`"version": 2`), 1),
		"unknown field":          bytes.Replace(base, []byte(`"version": 1`), []byte(`"version": 1, "surprise": true`), 1),
		"duplicate field":        bytes.Replace(base, []byte(`"version": 1`), []byte(`"version": 1, "version": 1`), 1),
		"duplicate nested field": bytes.Replace(base, []byte(`"kind": "native"`), []byte(`"kind": "native", "kind": "native"`), 1),
		"unknown capability":     bytes.Replace(base, []byte(`"operator:logical"`), []byte(`"operator:fiction"`), 1),
		"invalid binding kind":   bytes.Replace(base, []byte(`"kind": "native"`), []byte(`"kind": "raw-sql"`), 1),
		"noncanonical field":     bytes.Replace(base, []byte(`"kind": "native"`), []byte(`"Kind": "native"`), 1),
		"missing capabilities":   bytes.Replace(base, []byte(`"capabilities": [`), []byte(`"missingCapabilities": [`), 1),
		"null bindings":          bytes.Replace(base, []byte(`"bindings": [`), []byte(`"bindings": null, "unused": [`), 1),
		"unpaired surrogate":     bytes.Replace(base, []byte(`"people"`), []byte(`"\ud800"`), 1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Decode(data, config.Registry{}); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	if _, err := config.Decode(append(base, []byte(" {}")...), config.Registry{}); err == nil {
		t.Fatal("accepted two documents")
	}
	if !strings.Contains(string(base), `"version": 1`) {
		t.Fatal("version missing from document")
	}
	paired := bytes.Replace(base, []byte(`"people"`), []byte(`"\ud83d\ude00"`), 1)
	if _, err := config.Decode(paired, config.Registry{}); err != nil {
		t.Fatalf("valid surrogate pair was rejected: %v", err)
	}
}

func TestBindingSchemaAndResourceValidation(t *testing.T) {
	e, backend := basicConfig()
	backend.Bindings[0] = sql.Bind(hff.Path{"missing"}, sql.Column("people", "age"))
	if _, err := config.Encode(e, backend, config.Registry{}); err == nil || !strings.Contains(err.Error(), "undeclared field") {
		t.Fatalf("undeclared binding should fail: %v", err)
	}
	backend.Bindings[0] = sql.BindJSON(hff.Path{"age"}, sql.Column("people", "age"), sql.JSON,
		sql.WithTypeMetadata(sql.Column("people", "age_types")))
	if _, err := config.Encode(e, backend, config.Registry{}); err == nil || !strings.Contains(err.Error(), "metadata requires jsonb") {
		t.Fatalf("invalid JSON metadata should fail: %v", err)
	}
	e, backend = basicConfig()
	data, err := config.Encode(e, backend, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	tooLarge := append(append([]byte(nil), data...), bytes.Repeat([]byte(" "), config.MaxDocumentBytes)...)
	if _, err := config.Decode(tooLarge, config.Registry{}); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("oversized document should fail: %v", err)
	}
	deep := []byte(`{"version":1,"engine":{"schema":{"kind":"struct"},"capabilities":[]},"backend":{"kind":"postgresql","bindings":[]},"extra":`)
	deep = append(deep, bytes.Repeat([]byte("["), config.MaxDocumentDepth)...)
	deep = append(deep, '0')
	deep = append(deep, bytes.Repeat([]byte("]"), config.MaxDocumentDepth)...)
	deep = append(deep, '}')
	if _, err := config.Decode(deep, config.Registry{}); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("deep document should fail: %v", err)
	}

	profile := hff.NewStructType(hff.Field{Name: "age", Type: hff.PrimitiveType(hff.IntType)})
	e.Schema = hff.NewStructType(hff.Field{Name: "profile", Type: profile})
	backend.Bindings = []sql.Binding{
		sql.BindStruct(hff.Path{"profile"}, sql.Boolean(true), sql.Boolean(false),
			sql.Bind(hff.Path{"age"}, sql.Column("people", "age"))),
		sql.Bind(hff.Path{"profile", "age"}, sql.Column("people", "age")),
	}
	if _, err := config.Encode(e, backend, config.Registry{}); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("structured overlap should fail: %v", err)
	}
	e.Schema = hff.NewStructType(
		hff.Field{Name: "age", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "ratio", Type: hff.PrimitiveType(hff.FloatType)},
	)
	backend.Bindings = []sql.Binding{
		sql.Bind(hff.Path{"age"}, sql.Column("people", "value")),
		sql.Bind(hff.Path{"ratio"}, sql.Column("people", "value")),
	}
	if _, err := config.Encode(e, backend, config.Registry{}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting physical column should fail: %v", err)
	}
}

func TestSQLPredicateRejectsNULStringLiteral(t *testing.T) {
	e, backend := basicConfig()
	e.Schema = hff.NewStructType(
		hff.Field{Name: "age", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "profile", Type: hff.NewStructType(hff.Field{Name: "name", Type: hff.PrimitiveType(hff.StringType)})},
	)
	backend.Bindings = []sql.Binding{
		sql.Bind(hff.Path{"age"}, sql.Column("people", "age")),
		sql.BindStruct(hff.Path{"profile"},
			sql.Equal(sql.ColumnValue(sql.Column("people", "name"), hff.PrimitiveType(hff.StringType)), sql.Literal(hff.String("\x00"))),
			sql.Boolean(false), sql.Bind(hff.Path{"name"}, sql.Column("people", "name"))),
	}
	if _, err := config.Encode(e, backend, config.Registry{}); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("NUL SQL predicate literal should fail: %v", err)
	}
}

func TestEncodeDoesNotRepairInvalidUTF8(t *testing.T) {
	bad := string([]byte{0xff})
	for name, change := range map[string]func(*engine.Config, *sql.Config){
		"column": func(_ *engine.Config, backend *sql.Config) {
			backend.Bindings[0] = sql.Bind(hff.Path{"age"}, sql.Column("people", bad))
		},
		"schema field": func(e *engine.Config, _ *sql.Config) {
			e.Schema = hff.NewStructType(hff.Field{Name: bad, Type: hff.PrimitiveType(hff.IntType)})
		},
		"predicate column": func(e *engine.Config, backend *sql.Config) {
			e.Schema = hff.NewStructType(hff.Field{Name: "profile", Type: hff.NewStructType(hff.Field{Name: "name", Type: hff.PrimitiveType(hff.StringType)})})
			backend.Bindings = []sql.Binding{sql.BindStruct(hff.Path{"profile"},
				sql.IsNotNull(sql.ColumnValue(sql.Column("people", bad), hff.PrimitiveType(hff.StringType))),
				sql.Boolean(false), sql.Bind(hff.Path{"name"}, sql.Column("people", "name")))}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, backend := basicConfig()
			change(&e, &backend)
			if _, err := config.Encode(e, backend, config.Registry{}); err == nil {
				t.Fatal("invalid UTF-8 was replaced during encoding")
			}
		})
	}
}
