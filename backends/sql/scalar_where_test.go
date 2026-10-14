package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestDirectScalarWhereKeepsSimpleFiltersIndexable(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.Nullable(schema.Int("age")),
			schema.Nullable(schema.String("name")),
			schema.Nullable(schema.Bool("active")),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
		hffsql.Bind(hff.Path{"active"}, hffsql.Column("records", "active")),
	}
	for _, source := range []string{
		`age>18 name="Alice"`,
		`age>18|name="Alice"`,
		`(age>18 name="Alice")|active=true`,
		`age!=null`,
		`age=null`,
		`active`,
		`!active`,
		`!age>18`,
		`age:in(18,21)`,
		`name:in("Alice","Bob",null)`,
		`!active:in(true)`,
		`age:in(18,21) name="Alice"`,
		`name:contains("Ali")`,
		`name:contains("A","ice")`,
		`name:startswith("Al")`,
		`name:endswith("ce")`,
		`name:len>3`,
		`!name:len=0`,
		`!name:contains("Ali")`,
		`age>18 name:startswith("Al")`,
		`age>18 name:len>=5`,
		`name~"Alice"`,
		`name:contains(null)`,
		`name:contains("Ali",null)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(query.Where(), "SELECT") || strings.Contains(query.Where(), "OFFSET 0") || strings.Contains(query.Where(), "LATERAL") {
			t.Fatalf("simple predicate still has projection barriers: %s", query.Where())
		}
		if query.Where() == "FALSE" {
			if len(query.Args()) != 0 {
				t.Fatalf("constant false predicate kept unused parameters: %s %#v", source, query.Args())
			}
			continue
		}
		if !strings.Contains(query.Where(), `"records".`) {
			t.Fatalf("predicate lost direct column reference: %s", query.Where())
		}
	}
	for _, source := range []string{`age+1>20`, `name!:matches("Alice")`, `name:contains(name)`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), "SELECT") {
			t.Fatalf("noneligible predicate bypassed general lowering: %s", query.Where())
		}
	}
	program, err := e.Compile(ctx, `age>18`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(append(bindings, hffsql.Bind(hff.Path{"missing"}, hffsql.Column("records", "missing")))...))
	if err == nil {
		t.Fatal("direct lowering skipped validation of an invalid binding")
	}
}

func TestDirectScalarWhereLeavesNullableStructRepresentationToGeneralPath(t *testing.T) {
	ctx := context.Background()
	profile := schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(schema.Int("age")))))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(profile)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `profile.age>5`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindStruct(hff.Path{"profile"},
			hffsql.Boolean(true), hffsql.Boolean(false),
			hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "profile_age")))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where(), "SELECT") {
		t.Fatal("nullable parent state was flattened into a direct column predicate:", query.Where())
	}
}

func TestDirectStringPredicateValidatesArgumentsBeforeNullFolding(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Nullable(schema.String("name")))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`name:contains("\u0000",null)`,
		`name:contains(null,"\u0000")`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))))
		if err == nil || !strings.Contains(err.Error(), "NUL") {
			t.Fatalf("%s: expected PostgreSQL NUL error, got %v", source, err)
		}
	}
}
