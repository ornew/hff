package sql_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func mixedPredicateEngine(t testing.TB) (*engine.Engine, []hffsql.Binding) {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Int("age")), schema.Nullable(schema.String("name")),
		schema.Nullable(schema.Float("factor")), schema.Nullable(schema.String("raw")),
		schema.Nullable(schema.String("raw.name")),
		schema.Nullable(schema.Map("payload", schema.AnyType)),
		schema.Nullable(schema.List("tags", schema.StringType)),
		schema.Nullable(schema.Timestamp("created")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e, []hffsql.Binding{
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
		hffsql.Bind(hff.Path{"factor"}, hffsql.Column("records", "factor")),
		hffsql.Bind(hff.Path{"raw"}, hffsql.Column("records", "raw")),
		hffsql.Bind(hff.Path{"raw.name"}, hffsql.Column("records", "raw_name")),
		hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "payload"), hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "payload_meta"))),
		hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("records", "created")),
	}
}

func TestMixedPredicatesExposeDirectLeavesAndPreserveGenericSharing(t *testing.T) {
	ctx := context.Background()
	e, bindings := mixedPredicateEngine(t)
	for _, source := range []string{
		`age>18 name~"^A"`, `age>18|name~"^A"`,
		`!(age>18|name~"^A")`, `age>18 (name~"A"|factor+1.0>2.0)`,
		`age+1>10 name="Alice" age+2<30`,
		`age>18 raw:fromjson.price>10 raw:fromjson.active`,
		`raw:fromjson.price>10 age>18 raw:fromjson.active`,
		`payload:size>0 age>18 payload.left="a"`,
		`(age>18 name~"A")|(age<5 raw:fromjson.active)`,
		`(age>18 ` + "`raw.name`" + `:fromjson.price>10)|(age<5 raw:fromjson.active)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.HasPrefix(query.Where(), "(SELECT") {
			t.Fatalf("%s hid every direct leaf in a projection: %s", source, query.Where())
		}
		if strings.Contains(source, "raw:fromjson.price") && !strings.Contains(source, "raw.name") {
			if count := strings.Count(query.Where(), "IS JSON WITH UNIQUE KEYS"); count != 1 {
				t.Fatalf("%s: parsed document validation repeated %d times", source, count)
			}
		}
		if strings.Contains(source, "payload:size") {
			if count := strings.Count(query.Where(), "(value,pointer,position,prefix) AS ("); count != 1 {
				t.Fatalf("%s: metadata restoration repeated %d times", source, count)
			}
		}
		if strings.Contains(source, "age+1") {
			if !reflect.DeepEqual(query.Args(), []any{int64(1), int64(10), "Alice", int64(2), int64(30)}) {
				t.Fatal("interleaved parameters lost source order:", query.Args())
			}
			if count := strings.Count(query.Where(), `SELECT "records"."age" AS v`); count != 1 {
				t.Fatalf("generic siblings duplicated scalar projection: %d", count)
			}
		}
	}
}

func TestMixedPredicatesRetainOneScopeForOverlappingInputs(t *testing.T) {
	ctx := context.Background()
	e, bindings := mixedPredicateEngine(t)
	for _, source := range []string{
		`(age>18 raw:fromjson.price>10)|(age<5 raw:fromjson.active)`,
		`(age>18 payload.left="a")|(age<5 payload.right="b")`,
		`(age>18 factor+1.0>2.0)|(age<5 factor+2.0<3.0)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.HasPrefix(query.Where(), "(SELECT") {
			t.Fatalf("%s split scopes with overlapping input", source)
		}
		if strings.Contains(source, "fromjson") && strings.Count(query.Where(), "IS JSON WITH UNIQUE KEYS") != 1 {
			t.Fatal("fallback duplicated parsed JSON")
		}
	}
	program, err := e.Compile(ctx, `age>18 name~"A"`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(append(bindings, hffsql.Bind(hff.Path{"missing"}, hffsql.Column("records", "missing")))...))
	if err == nil {
		t.Fatal("composition skipped binding validation")
	}
	program, err = e.Compile(ctx, `age>18|created:year("Asia/Tokyo")>2020`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
	if err == nil {
		t.Fatal("composition skipped unsupported backend operation")
	}
}
