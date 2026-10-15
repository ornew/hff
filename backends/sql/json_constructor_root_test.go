package sql_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func constructorRootSources(itemRoleFixture) []string {
	const values = `[labels.app,labels.z]`
	const types = `[labels.app:type,labels.z:type]:json`
	return []string{
		values + `[0]:type=labels.app:type`,
		values + `[-1]:type=labels.z:type`,
		values + `[labels:size%2]:type:in("bytes","duration","timestamp","string","null","number","bool","list","object")`,
		values + `:map(v=>v:type):json=` + types,
		values + `:map(v=>v:json):json=[labels.app:json,labels.z:json]:json`,
		values + `:contains(labels.app)`,
		values + `:unique`,
		values + `:distinct:size:exists`,
		values + `=` + values,
		values + `!=[labels.z,labels.app]`,
		values + `:filter(v=>v:exists):map(v=>v:type):json:exists`,
		values + `:flatmap(v=>[v,v]):map(v=>v:type):json:exists`,
		`{"entries":` + values + `}.entries:map(v=>v:type):json=` + types,
		`[{"value":labels.app},{"value":labels.z}]:map(v=>v.value:type):json=` + types,
		`[[labels.app],[labels.z]]:flatmap(xs=>xs):map(v=>v:type):json=` + types,
		values + `:find(v=>v:type=labels.z:type):type=labels.z:type`,
		values + `:sum(v=>v:type:len):exists`,
		`[labels!.app,labels.z]:size:exists`,
		`[labels.app!,labels.z]:distinct:exists`,
		values + `:map(v=>` + values + `:find(v=>v:type="string"):exists v:exists):json:exists`,
	}
}

func TestConstructorRootBuild(t *testing.T) {
	for _, tc := range append(itemRoleFixtures(), itemAnyFixtures()...) {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range constructorRootSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestConstructorRootNativeBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Bytes("asset"), schema.String("name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`[asset,name][0]:type="bytes"`, `[asset,name]:contains(name)`, `[asset,name]:map(v=>v:type):json:exists`, `[asset,name]:distinct:size=2`, `[asset,name]=[asset,name]`, `[name,"a"]:contains(name)`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"asset"}, hffsql.Column("asset")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("name")))); err != nil {
			t.Error(source, err)
		}
	}
}

func TestConstructorRootCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Bytes("asset"), schema.String("name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var elements []string
	for i := 0; i < 24; i++ {
		elements = append(elements, fmt.Sprintf(`{"v%d":name}`, i))
	}
	elements = append(elements, `{"v0":asset}`)
	sources := []string{`[` + strings.Join(elements, ",") + `]:map(v=>v:type):json:exists`, `[{"a":[asset,name]},{"a":[name,asset]}]:map(v=>v.a):flatmap(v=>v):distinct:size>0`}
	for _, source := range sources {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"asset"}, hffsql.Column("asset")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("name")))}
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, options...); err != nil {
			t.Fatal(err)
		}
		for _, after := range []int64{1, baseline.checks.Load() / 4, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
			ctx, cancel := context.WithCancel(context.Background())
			q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, options...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("partial %v,error %v", q, err)
			}
		}
	}
}

func TestConstructorRootRejectsAmbiguousStoredChild(t *testing.T) {
	for _, tag := range []hff.Type{schema.BytesType, schema.DurationType, schema.TimestampType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", schema.Union(schema.StringType, tag)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`[value,"a"][0]:type="string"`, `[value,"a"]:map(v=>v:type):json:exists`, `[value,"a"]:filter(v=>false):size=0`} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), format)))
				requireCode(t, err, hff.BackendError)
			}
		}
	}
}
