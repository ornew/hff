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

func compositeEngine(t testing.TB) *engine.Engine {
	t.Helper()
	profile := schema.Struct(
		schema.Nullable(schema.String("name")), schema.Nullable(schema.Int("age")),
		schema.Nullable(schema.Field("father", schema.Struct(schema.Nullable(schema.String("name"))))),
		schema.Nullable(schema.List("scores", schema.NullableType(schema.IntType))),
		schema.Nullable(schema.List("factors", schema.FloatType)),
		schema.Nullable(schema.List("people", schema.NullableType(schema.Struct(schema.Nullable(schema.String("name")))))),
		schema.Optional(schema.Nullable(schema.String("service.name"))),
	)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("profile", profile)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func compositeQueries() []string {
	return []string{
		`profile`, `profile=null`, `profile!=null`, `profile:exists`, `profile:type="object"`,
		`profile!:exists`, `profile!.name=null`, `!profile!.name=null`,
		`profile.name="Alice"`, `profile.age>=18`, `profile:size=7`, `profile:has("name")`,
		`profile.age=null`, `profile.age:exists`, `profile.father.name=null`, `profile.father.name:exists`,
		`profile:get("name")="Alice"`, `profile:has("service.name")`, "profile.`service.name`=\"auth\"",
		`profile.father=null`, `profile.father:exists`, `profile.father:has("name")`,
		`profile!.father!.name=null`, `!profile!.father!.name=null`,
		`profile.scores=[10,20,null]`, `profile.scores:any(x=>x>10)`, `profile.scores:all(x=>x!:exists)`,
		`profile.scores:map(x=>x+1)=[11,21,null]`, `profile.scores:foldl(0,(acc,x)=>x)=null`,
		`profile.scores:sort=null`, `profile.scores:contains(null)`, `profile.scores[-1]=null`,
		`profile.factors:tojson="[-0.0,1.0]"`, `profile.factors[0]:tostring="-0.0"`,
		`profile.factors:map(x=>x):tojson="[-0.0,1.0]"`, `profile.factors=[-0.0,1.0]`,
		`profile:tojson:contains("service.name")`, `profile=profile`,
		`profile={"name":null,"age":null,"father":null,"scores":null,"factors":null,"people":null,"service.name":null}`,
		`profile.father={"name":null}`, `profile.father={}`, `profile:keys[0]="age"`,
		`profile.people:any(x=>x.name="A")`, `profile.people:all(x=>x!:exists)`,
		`profile.people=[{"name":"A"},null,{"name":null}]`,
		`profile.people[1]=null`, `profile.people[2]={"name":null}`,
		`profile.people:map(x=>x.name)=["A",null,null]`,
		`profile.people:map(x=>x!.name)=null`, `!profile.people:map(x=>x!.name)=null`,
		`profile.people:tojson="[{\"name\":\"A\"},null,{\"name\":null}]"`,
		`profile.people:contains({"name":null},null)`, `profile.people:unique`,
	}
}

func TestCompositeFieldPathsConvertOnlySelectedAttributes(t *testing.T) {
	e := compositeEngine(t)
	for _, source := range []string{`profile.age>=18`, `profile.father.name="Alice"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		query, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))))
		if err != nil {
			t.Fatal(err)
		}
		for _, unrelated := range []string{"json_build_object", "generate_subscripts", `"scores"`, `"people"`, `"factors"`} {
			if strings.Contains(query.Where(), unrelated) {
				t.Fatalf("%s converts unrelated composite attributes: %s", source, query.Where())
			}
		}
	}
}

func TestCompositeBindingsBuildTypedOperations(t *testing.T) {
	e := compositeEngine(t)
	for _, source := range compositeQueries() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))))
		if err != nil {
			t.Fatal(source, err)
		}
	}
}

func TestCompositeBindingConfiguration(t *testing.T) {
	ctx := context.Background()
	e := compositeEngine(t)
	p, err := e.Compile(ctx, `profile.name="Alice"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bindings := range [][]hffsql.Binding{
		{hffsql.BindComposite(hff.Path{"profile", "name"}, hffsql.Column("name"))},
		{hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("profile")), hffsql.Bind(hff.Path{"profile", "name"}, hffsql.Column("name"))},
	} {
		_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		requireCode(t, err, hff.ConfigurationError)
	}
}

func TestCompositeBindingRejectsUnsupportedRepresentations(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []schema.Type{
		schema.MapOf(schema.StringType),
		schema.BytesType,
		schema.TimestampType,
		schema.DurationType,
		schema.AnyType,
		schema.ListOf(schema.ListOf(schema.IntType)),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("profile", schema.Struct(schema.Field("value", typ))))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `profile:exists`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("profile"))))
		requireCode(t, err, hff.BackendError)
	}
}
