package sql_test

import (
	"context"
	"reflect"
	"strings"
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
		`people:any(x=>x.age>=18 x.age<=100 x.age:exists)`,
		`people:any(x=>x.age>=18 people:any(x=>x.age<=100))`,
		`people:any(x=>x.name=null | x!.name=null)`,
		`scores:map(x=>x+x)[0]=20`,
		`scores:foldl(0,(acc,x)=>acc+acc+x)>=0`,
		`people:any(x=>profile.age>=0 people:any(y=>profile.age>y.age) profile.age<=100)`,
		`profile.age>=18 profile.age<=100 profile.age:exists`,
		`profile.name:exists | profile!.name=null`,
		`profile.father.name=null | profile.father.name="Bob"`,
		`scores:any(x=>profile.age>x) profile.age>=0`,
		`profile.age>=0 scores:any(x=>profile.age>x)`,
		`scores:map(x=>profile.age+x)[0]=profile.age+10`,
		`scores:foldl(0,(acc,x)=>acc+profile.age)=profile.age*scores:size`,
		`people:any(x=>x.name="Alice") people:any(x=>x.name="Bob")`,
		`scores:map(x=>-0.0):json="[-0.0,-0.0,-0.0]"`,
		`scores:map(x=>-0.0)[0]:string="-0.0"`,
		`factors:map(x=>-0.0):json="[-0.0]"`,
		`scores:map(x=>{"zero":-0.0}):json="[{\"zero\":-0.0},{\"zero\":-0.0},{\"zero\":-0.0}]"`,
		`scores:foldl(-0.0,(acc,x)=>acc):string="-0.0"`,
		`scores:json="[10,20,null]"`, `scores:string="null"`, `scores!:json="null"`, `!scores!:json="null"`,
		`labels:json="{\"app\":\"auth\",\"team\":null}"`, `labels:string="{}"`,
		`profile:json="{\"age\":18,\"name\":\"Alice\"}"`, `people:json:contains("Alice")`,
		`groups:json="[[10,20],[],null,[null]]"`, `scores:map(x=>x>10):json="[false,true,false]"`,
		`scores:map(x=>[x,"a"]):json="[[10,\"a\"],[20,\"a\"],[null,\"a\"]]"`,
		`profile.name:string="Alice"`, `profile.age:string="18"`, `profile.name:json="\"Alice\""`,
		`scores:map(x=>{"n":x,"s":"a\nb\tc\\\"<>&/日本語"}):json:contains("日本語")`,
		`labels:items:json="[[\"app\",\"auth\"],[\"team\",null]]"`,
		`scores:foldl(0,(acc,x)=>acc+x)=30`, `scores:foldl(0,(acc,x)=>acc+x)=null`,
		`scores:foldr(0,(x,acc)=>x-acc)=26`, `scores:foldl(0,(acc,x)=>acc-x)=-34`,
		`scores:foldl(null,(acc,x)=>x)=null`, `scores:foldr(null,(x,acc)=>x)=null`,
		`scores:foldl(null,(acc,x)=>x)=scores[-1]`, `scores:foldr(null,(x,acc)=>x)=scores[0]`,
		`scores:foldl(0,(acc,x)=>x!)=null`, `!scores:foldl(0,(acc,x)=>x!)=null`,
		`scores:foldl(profile!.age,(acc,x)=>x)=null`, `!scores:foldl(profile!.age,(acc,x)=>x)=null`,
		`scores:foldl([],(acc,x)=>acc:concat([x]))=scores`, `scores:foldr([],(x,acc)=>acc:concat([x]))[0]=scores[-1]`,
		`groups:foldl(0,(acc,xs)=>acc+xs:foldl(0,(a,x)=>a+x))=null`,
		`people:any(x=>scores:foldl(0,(acc,y)=>acc+y)=x.age)`, `scores:foldl(0,(acc,x)=>acc+x+profile.age)>0`,
		`labels:keys:foldl("",(acc,x)=>acc:concat(x))="appteam"`,
		`labels:keys:foldr("",(x,acc)=>x:concat(acc))="appteam"`, `scores:foldl(false,(acc,x)=>acc|x>10)`,
		`scores:foldl(0,(acc,x)=>x+1)=scores[-1]+1`,
		`scores:map(x=>x)=scores`, `scores:map(x=>x+1)=[11,21,null]`, `scores:map(x=>null)=[null,null,null]`,
		`scores:map(x=>x!)=null`, `!scores:map(x=>x!)=null`, `scores:map(x=>[x,null]):size=scores:size`,
		`people:map(x=>x.age)=[18,null]`, `people:map(x=>x!.age)=null`, `!people:map(x=>x!.age)=null`,
		`people:map(x=>{"name":x.name,"age":x.age}):size=people:size`, `scores:map(x=>x>10)=[false,true,false]`,
		`groups:map(xs=>xs:map(x=>x+1))=[[11,21],[],null,[null]]`,
		`people:map(x=>scores:map(y=>y+x.age)):size=people:size`, `people:map(x=>scores:map(x=>x+1)):size=people:size`,
		`groups:flatmap(xs=>xs)=null`, `groups:flatmap(xs=>xs!)=null`, `!groups:flatmap(xs=>xs!)=null`,
		`groups:flatmap(xs=>xs:concat([xs[0]!]))=null`, `!groups:flatmap(xs=>xs:concat([xs[0]!]))=null`,
		`scores:map(x=>x+1)[0]=null`, `scores:map(x=>profile!.age)=[]`,
		`scores:flatmap(x=>[x,null]):size=scores:size*2`, `scores:flatmap(x=>[x])=scores`,
		`scores:flatmap(x=>[x!])=null`, `scores:flatmap(x=>[])=[]`, `scores:flatmap(x=>scores):size=scores:size*scores:size`,
		`scores:map(x=>[x,null]):flatmap(xs=>xs)=[10,null,20,null,null,null]`,
		`people:flatmap(x=>[x,x]):size=people:size*2`, `people:any(x=>scores:map(y=>y+x.age):contains(28))`,
		`scores:sort=[]`, `scores:sort=null`, `scores:sort=scores`, `scores:sort=[2,2,10,20]`, `scores:sort:has(-1)`,
		`factors:sort=[1.0,1.0]`, `factors:sort=factors`, `labels:keys:sort=labels:keys`, `labels:values:sort=null`,
		`scores:concat([30,null])[-1]=null`, `scores:concat([])=scores`, `scores:concat(scores):size=scores:size*2`,
		`scores:concat([profile!.age])=null`, `!scores:concat([profile!.age])=null`, `scores:concat(null)=null`,
		`scores:concat(null)[0]=null`, `scores:concat(null):size=null`, `scores:concat(null):sort=null`,
		`scores:concat(["z","a"]):sort=["a","z"]`, `scores:concat(["z","a"]):sort=null`, `scores:concat([true]):sort=null`,
		`groups:concat([[],null]):size=groups:size+2`, `groups:concat([])=groups`,
		`people:concat([profile])[-1]=profile`, `people:any(x=>scores:concat([x.age]):sort=null)`,
		`scores:contains(10,20,null)`, `scores:contains(null)`, `scores:contains(9007199254740992.0)`, `scores:contains(9007199254740993)`,
		`scores:contains(9223372036854775808.0)`, `scores:contains(-9223372036854775808.0)`, `scores:contains("10")`,
		`scores:contains(null,profile!.age)`, `!scores:contains(null,profile!.age)`, `scores:unique`, `groups:unique`, `people:unique`,
		`groups:contains([10,20],null,[])`, `people:contains({"name":"Alice","age":18})`,
		`labels:items:contains(["team",null])`, `labels:items:unique`, `labels:keys:unique`,
		`profile.age:in(18,9007199254740992.0,null)`, `profile.name:in("Alice",null)`, `profile.name:in("Alice",profile!.father!.name)`,
		`profile.age:in(null,profile!.name!)`, `factors:unique`, `labels:values:unique`,
		`profile:in({"name":"Alice","age":18},null)`, `scores:in([], [10,20,null], null)`, `scores:in(scores)`,
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

func TestCallbackPathSharingStaysInsideItsBindingScope(t *testing.T) {
	e := jsonEngine(t)
	for _, tc := range []struct {
		source   string
		keyCount int
	}{
		{`people:any(x=>x.age>=18 x.age<=100 x.age:exists)`, 1},
		{`people:any(x=>x.age>=18) people:any(x=>x.age<=100)`, 2},
		{`people:any(x=>x.age>=18 people:any(x=>x.age<=100))`, 2},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(format)...))
			if err != nil {
				t.Fatal(err)
			}
			keys := 0
			for _, arg := range q.Args() {
				if name, ok := arg.(string); ok && name == "age" {
					keys++
				}
			}
			if keys != tc.keyCount {
				t.Fatalf("%s: age key bound %d times, want %d", tc.source, keys, tc.keyCount)
			}
		}
	}
}

func TestRepeatedJSONPathsShareOuterExtraction(t *testing.T) {
	p, err := jsonEngine(t).Compile(context.Background(), `profile.age>=18 profile.age<=100 profile.age:exists`)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(format)...))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(q.Where(), `"records"."profile"`) != 1 {
			t.Fatal("root JSON document extracted repeatedly:", q.Where())
		}
		if !reflect.DeepEqual(q.Args(), []any{"age", int64(18), int64(100)}) {
			t.Fatal("repeated field name bound more than once or value arguments changed:", q.Args())
		}
	}
}

func TestIdenticalListGuardUsesTheDeclaredSchema(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:[list[int]]:exists`)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(q.Where(), "_array_elements") {
			t.Fatal("identical schema guard rescans list elements:", q.Where())
		}
		p, err := e.Compile(ctx, `items:[list[string]]:exists`)
		if err != nil {
			t.Fatal(err)
		}
		q, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(q.Where(), "_array_elements") {
			t.Fatal("different element constraint lost its runtime checks:", q.Where())
		}
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
	for _, source := range []string{`scores:foldl(0.0,(acc,x)=>x)>0`, `scores:foldl("",(acc,x)=>x):exists`} {
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
	p, err := jsonEngine(t).Compile(ctx, `people:any(x=>scores:any(y=>y<x.age) x.name="Alice" scores:contains(x.age,null)) groups:all(xs=>xs:all(x=>x!=null) xs:unique) scores:map(x=>[x,null]):flatmap(xs=>xs):contains(null) scores:foldl([],(acc,x)=>acc:concat([x]))=scores`)
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

func TestJSONFloatSerializationRepresentation(t *testing.T) {
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range []string{`factors:json="[-0.0]"`, `factors[0]:string="-0.0"`, `factors:map(x=>x):json="[-0.0]"`} {
			p, err := jsonEngine(t).Compile(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(jsonBindings(format)...))
			if format == hffsql.JSONB {
				requireCode(t, err, hff.BackendError)
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
}
