package sql_test

import (
	"context"
	"reflect"
	"sync"
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
		schema.Nullable(schema.List("groups", schema.NullableType(schema.ListOf(schema.NullableType(schema.IntType))))),
		schema.Nullable(schema.List("people", schema.NullableType(schema.Struct(schema.Nullable(schema.String("name")), schema.Nullable(schema.Int("age")), schema.Nullable(schema.Field("father", schema.Struct(schema.Nullable(schema.String("name"))))))))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func jsonBindings(format hffsql.JSONFormat) []hffsql.Binding {
	var result []hffsql.Binding
	for _, name := range []string{"profile", "scores", "labels", "factors", "groups", "people"} {
		result = append(result, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format))
	}
	return result
}

func jsonQueries() []string {
	return []string{
		`scores=[10,20,null]`, `scores!=[10,20,null]`, `scores=[20,10,null]`, `scores=[10,20]`, `scores=[]`, `scores=[null]`,
		`scores=[10.0,20.0,null]`, `scores=[9007199254740992.0]`, `scores=["10",20,null]`, `scores=scores`, `scores!=scores`,
		`scores=[9007199254740993]`, `scores=[9223372036854775808.0]`, `scores=[-9223372036854775808.0]`, `scores!=[9007199254740992.0]`,
		`groups=[[10,20],[],null,[null]]`, `groups=[[20,10],[],null,[null]]`, `groups=groups`,
		`labels={"team":null,"app":"auth"}`, `labels={"app":"auth"}`, `labels={"team":null}`, `labels={}`, `labels=labels`,
		`profile={"name":"Alice","age":18}`, `profile={"name":"Alice","age":18.0}`, `profile={"age":18,"name":"Alice","father":null}`,
		`profile!.father!={"name":"Bob"}`, `!profile!.father!={"name":"Bob"}`, `profile=profile`, `people=[profile,null]`,
		`factors=[1]`, `factors=[0.0]`, `labels:keys=["app","team"]`, `labels:values=["auth",null]`,
		`labels:items=[["app","auth"],["team",null]]`, `people:any(x=>x={"name":"Alice","age":18})`,
		`scores=[profile!.age]`, `!scores=[profile!.age]`, `labels={"app":profile!.name}`, `labels:items=labels:items`,
		`people:any(x=>x.age>=18 x.name="Alice")`, `people:all(x=>x!.age>=0)`,
		`people:any(x=>x.father!.name="Bob"|x.name="Alice")`, `people:any(x=>scores:any(y=>y<x.age))`,
		`people:any(x=>people:any(x=>x.name="Alice"))`, `people:any(x=>!x!.age)`,
		`profile.father.name="Bob"`, `profile!.father!.name=null`, `profile.father:has("name")`,
		`profile.age=9007199254740992.0`, `profile.age>9007199254740992.0`, `profile.age<9223372036854775808.0`,
		`profile.name="Alice"`, `profile.age>=18`, `profile=null`, `profile!.name=null`, `!profile!.name=null`,
		`profile:has("name")`, `profile:get("name")="Alice"`, `profile:size=2`, `profile`, `profile:exists`,
		`scores:any(x=>x>10)`, `scores:all(x=>x>0)`, `scores:any(x=>x=null)`, `scores:all(x=>x!:exists)`,
		`scores:any(x=>profile.age>x profile.name="Alice")`, `scores:any(x=>scores:all(y=>y>x))`,
		`scores:any(x=>scores:any(x=>x=20))`, `groups:any(xs=>xs:any(x=>x>10))`, `groups:all(xs=>xs:all(x=>x!:exists))`,
		`groups:any(xs=>xs:all(x=>x>0)|xs:size=0)`, `!groups:any(xs=>xs!:exists)`,
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
	for _, source := range []string{`labels:tojson="{}"`, `scores:unique`, `scores:sort[0]=10`, `scores:contains(10)`, `scores=[10,20.0]`} {
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

func TestJSONCallbacksConcurrentBuildsPreserveScopes(t *testing.T) {
	ctx := context.Background()
	p, err := jsonEngine(t).Compile(ctx, `people:any(x=>scores:any(y=>y<x.age) x.name="Alice") groups:all(xs=>xs:all(x=>x!=null))`)
	if err != nil {
		t.Fatal(err)
	}
	build := func() (*hffsql.Query, error) {
		return hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(hffsql.JSONB)...))
	}
	expected, err := build()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				query, err := build()
				if err != nil {
					t.Error(err)
					return
				}
				if query.Where() != expected.Where() || !reflect.DeepEqual(query.Args(), expected.Args()) {
					t.Error("callback scope leaked between builds")
					return
				}
			}
		})
	}
	workers.Wait()
}
