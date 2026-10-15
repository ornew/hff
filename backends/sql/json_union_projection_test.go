package sql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func unionProjectionObject(bad hff.Type) hff.Type {
	return schema.Struct(
		schema.Nullable(schema.String("safe")),
		schema.Nullable(schema.Int("n")),
		schema.Nullable(schema.List("nums", schema.IntType)),
		schema.Optional(schema.String("a/b~雪")),
		schema.Field("bad", bad),
	)
}

func unionProjectionTypes() []struct {
	name string
	typ  hff.Type
} {
	object := unionProjectionObject(schema.Union(schema.StringType, schema.BytesType))
	return []struct {
		name string
		typ  hff.Type
	}{
		{"struct", object},
		{"struct-or-int", schema.Union(object, schema.IntType)},
		{"struct-or-list", schema.Union(object, schema.ListOf(schema.BytesType))},
		{"two-structs", schema.Union(unionProjectionObject(schema.StringType), unionProjectionObject(schema.BytesType))},
	}
}

func unionProjectionSources() []string {
	return []string{
		`data.safe="ok"`, `data["safe"]="ok"`, `data:get("safe")="ok"`,
		`data!.safe="ok"`, `data!["safe"]="ok"`, `data!:get("safe")="ok"`,
		`data.safe=null`, `data.safe:exists`, `data.safe:len>1`,
		`data:get("safe"):len>1`, `data["safe"]:len>1`,
		`data:has("safe")`, `data:keys:contains("safe")`, `data:size>0`,
		`data.nums:sum>0`, `data["nums"]:sum>0`, `data:get("nums"):sum>0`,
		`data.nums:json="[1,2,3]"`, `data:get("nums")[-1]=3`,
		`data.nums:count(x=>x>1)=2`, `data.nums:any(x=>x>1)`,
		`data.nums:map(x=>x+1):json="[2,3,4]"`,
		`data.nums:filter(x=>x>1):json="[2,3]"`,
		`data.nums:find(x=>x>1)=2`,
		`data.` + "`a/b~雪`" + `="yes"`, `data:get("a/b~雪")="yes"`,
		`data.safe="ok" data["safe"]:exists | data.n=null`,
		`[data.safe,data:get("safe")]:json:exists`,
	}
}

func unionProjectionCallbackSources() []string {
	return []string{
		`data:any(x=>x.safe="ok")`,
		`data:all(x=>x.safe:exists)`,
		`data:count(x=>x["safe"]="ok")>0`,
		`data:count(x=>x!:get("safe")="ok")>0`,
		`data:sum(x=>x.n)>0`, `data:min(x=>x.n)=1`,
		`data:max(x=>x.n)=3`, `data:avg(x=>x.n)=2.0`,
		`data:map(x=>x.safe):json:exists`,
		`data:map(x=>x["safe"]):json:exists`,
		`data:map(x=>x:get("safe")):json:exists`,
		`data:map(x=>x.nums):flatmap(xs=>xs):sum>0`,
		`data:count(x=>x.nums:any(x=>x>1))>0`,
		`data:any(x=>["ok"]:any(y=>x.safe=y))`,
	}
}

func unionProjectionOptions(format hffsql.JSONFormat, metadata bool) []hffsql.Option {
	var settings []hffsql.JSONBindingOption
	if metadata {
		settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	}
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, settings...))}
}

func TestUnionProjectionBuild(t *testing.T) {
	for _, tc := range unionProjectionTypes() {
		for _, callbacks := range []bool{false, true} {
			typ, sources := tc.typ, unionProjectionSources()
			if callbacks {
				typ, sources = schema.ListOf(schema.Union(tc.typ, schema.NullType)), unionProjectionCallbackSources()
			}
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", typ)))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range sources {
				p, err := e.Compile(context.Background(), source)
				if err != nil {
					t.Fatal(tc.name, source, err)
				}
				for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
					for _, metadata := range []bool{false, true} {
						if format == hffsql.JSON && metadata {
							continue
						}
						if _, err := hffsql.Build(context.Background(), p, unionProjectionOptions(format, metadata)...); err != nil {
							t.Errorf("%s callbacks=%t %s metadata=%t %s: %v", tc.name, callbacks, format, metadata, source, err)
						}
					}
				}
			}
		}
	}
}

func TestUnionProjectionRejectsConsumedAmbiguity(t *testing.T) {
	for _, tc := range unionProjectionTypes() {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", tc.typ)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{
			`data.bad:type="string"`, `data["bad"]:type="bytes"`, `data:get("bad"):exists`,
			`data:json:exists`, `data:string:exists`, `data=data`,
			`data.safe="ok" data:json:exists`, `data:get("safe")="ok" data=data`,
			`[data]:map(x=>x):json:exists`,
		} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, unionProjectionOptions(format, format == hffsql.JSONB)...)
				requireCode(t, err, hff.BackendError)
			}
		}
	}
}

func TestUnionProjectionCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.ListOf(unionProjectionTypes()[1].typ)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `data:map(x=>x.nums):flatmap(xs=>xs):sum>0`)
	if err != nil {
		t.Fatal(err)
	}
	options := unionProjectionOptions(hffsql.JSON, false)
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("partial %v, error %v", q, err)
		}
	}
}

func TestUnionProjectionNumericProvenance(t *testing.T) {
	object := schema.Struct(schema.Field("n", schema.Union(schema.IntType, schema.FloatType)), schema.Field("bad", schema.Union(schema.StringType, schema.BytesType)))
	for _, callbacks := range []bool{false, true} {
		typ := schema.Union(object, schema.IntType)
		sources := []string{`data.n:[int]=1`, `data["n"]:[float]=1.0`, `data:get("n"):json:exists`}
		if callbacks {
			typ = schema.ListOf(typ)
			sources = []string{`data:any(x=>x.n:[int]=1)`, `data:map(x=>x["n"]):any(x=>x:[float]=1.0)`, `data:map(x=>x:get("n")):json:exists`}
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range sources {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				for _, metadata := range []bool{false, true} {
					if format == hffsql.JSON && metadata {
						continue
					}
					_, err := hffsql.Build(context.Background(), p, unionProjectionOptions(format, metadata)...)
					if format == hffsql.JSONB && !metadata {
						requireCode(t, err, hff.BackendError)
					} else if err != nil {
						t.Error(source, format, metadata, err)
					}
				}
			}
		}
	}
}

func TestUnionProjectionValidatesUnusedCallbacks(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.ListOf(unionProjectionTypes()[1].typ)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`data:filter(x=>false):map(x=>x.safe:concat("\u0000")):json:exists`,
		`data:filter(x=>false):any(x=>x:get("safe"):concat("\u0000"))`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, unionProjectionOptions(hffsql.JSON, false)...)
		requireCode(t, err, hff.BackendError)
	}
}
