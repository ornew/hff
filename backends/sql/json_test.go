package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func jsonEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Field("profile", schema.Struct(schema.Nullable(schema.String("name")), schema.Nullable(schema.Int("age")), schema.Nullable(schema.Field("father", schema.Struct(schema.Nullable(schema.String("name")))))))),
		schema.Nullable(schema.List("scores", schema.NullableType(schema.IntType))),
		schema.Nullable(schema.Map("labels", schema.NullableType(schema.StringType))),
		schema.Nullable(schema.List("factors", schema.FloatType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func jsonBindings(format hffsql.JSONFormat) []hffsql.Binding {
	var result []hffsql.Binding
	for _, name := range []string{"profile", "scores", "labels", "factors"} {
		result = append(result, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format))
	}
	return result
}

func jsonQueries() []string {
	return []string{
		`profile.father.name="Bob"`, `profile!.father!.name=null`, `profile.father:has("name")`,
		`profile.age=9007199254740992.0`, `profile.age>9007199254740992.0`, `profile.age<9223372036854775808.0`,
		`profile.name="Alice"`, `profile.age>=18`, `profile=null`, `profile!.name=null`, `!profile!.name=null`,
		`profile:has("name")`, `profile:get("name")="Alice"`, `profile:size=2`, `profile`, `profile:exists`,
		`scores[0]=10`, `scores[-1]=null`, `scores[1.0]=20`, `scores[0.5]=null`, `scores[2147483648]=null`,
		`scores:size=3`, `scores:has(-1)`, `scores:has(99)`, `scores:has(0.5)=null`, `scores`,
		`scores:[list[int|null]]:size=3`, `scores:[list[string]]:exists`, `!scores:[list[string]]:exists`,
		`labels.app="auth"`, `labels["app"]="auth"`, `labels:get("absent")=null`, `labels:has("team")`,
		`labels:keys[0]="app"`, `labels:values[-1]=null`, `labels:items[0]:size=2`, `labels:keys:size=2`, `labels:values:size=2`, `labels:items:size=2`,
		`labels:size=2`, `labels:type="object"`, `labels`, `labels:[map[string|null]]:exists`,
		`factors[0]>0`, `factors[0]:[int]:exists`, `factors[0]:[float]:exists`, `factors[0]=1.0`, `factors[0]:type="number"`, `factors:[list[float]]:exists`,
	}
}

func TestJSONBindingBuildsTypedAccess(t *testing.T) {
	e := jsonEngine(t)
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range jsonQueries() {
			t.Run(string(format)+"/"+source, func(t *testing.T) {
				p, err := e.Compile(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(format)...))
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestJSONBindingRejectsConflictingPhysicalMappings(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(schema.Int("age"))), schema.Int("age"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `data.age>10`)
	if err != nil {
		t.Fatal(err)
	}
	jsonBinding := hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("payload"), hffsql.JSONB)
	for _, bs := range [][]hffsql.Binding{
		{hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("payload"), "unknown")},
		{jsonBinding, hffsql.Bind(hff.Path{"data", "age"}, hffsql.Column("age"))},
		{hffsql.Bind(hff.Path{"data", "age"}, hffsql.Column("age")), jsonBinding},
		{jsonBinding, hffsql.Bind(hff.Path{"age"}, hffsql.Column("payload"))},
		{hffsql.Bind(hff.Path{"age"}, hffsql.Column("payload")), jsonBinding},
		{jsonBinding, hffsql.BindJSON(hff.Path{"age"}, hffsql.Column("payload"), hffsql.JSON)},
		{jsonBinding, hffsql.BindJSON(hff.Path{"age"}, hffsql.Column("payload"), hffsql.JSONB)},
	} {
		_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bs...))
		requireCode(t, err, hff.ConfigurationError)
	}
}

func TestJSONUnsupportedSemanticsAreExplicitBuildErrors(t *testing.T) {
	ctx := context.Background()
	for _, source := range []string{`scores=[10,20]`, `labels:tojson="{}"`, `scores:any(x=>x>10)`, `scores:unique`, `scores:sort[0]=10`, `scores:contains(10)`} {
		p, err := jsonEngine(t).Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(hffsql.JSONB)...))
		requireCode(t, err, hff.BackendError)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("x", schema.Union(schema.IntType, schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `x:[int]=1`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"x"}, hffsql.Column("x"), hffsql.JSONB)))
	requireCode(t, err, hff.BackendError)
}
