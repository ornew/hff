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

func structRootUnionSchema(key hff.Type) hff.Type {
	fields := dynamicStructSchema().Fields()
	fields[0].Type = schema.Union(fields[0].Type, schema.IntType, schema.ListOf(schema.BytesType))
	fields[1] = schema.Nullable(schema.Field("key", key))
	return schema.Struct(fields...)
}

func TestStructRootUnionSelectedSchemas(t *testing.T) {
	object := schema.Struct(schema.Bytes("asset"), schema.String("text"))
	for _, key := range []hff.Type{schema.StringType, schema.AnyType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(object, schema.ListOf(schema.Union(schema.StringType, schema.BytesType)))), schema.Field("key", key))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`data[key]:type="bytes"`, `data:get(key):type="bytes"`, `data:values:any(v=>v:type="bytes")`} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, dynamicStructOptions(format, true)...)
				if key.Kind() == hff.AnyType && source == `data[key]:type="bytes"` {
					// A numeric runtime key can observe the ambiguous list element.
					requireCode(t, err, hff.BackendError)
				} else if err != nil {
					t.Error(key, source, format, err)
				}
			}
		}
	}
	for _, typ := range []hff.Type{
		schema.Union(object, schema.AnyType),
		schema.Union(object, schema.MapOf(schema.StringType)),
		schema.Union(object, schema.Struct(schema.String("asset"), schema.Bytes("text"))),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`data[key]:type="bytes"`, `data:get(key):type="bytes"`, `data:values:map(v=>v:type):json:exists`} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, dynamicStructOptions(format, true)...)
				requireCode(t, err, hff.BackendError)
			}
		}
	}
}

func TestStructRootUnionCancellation(t *testing.T) {
	fields := []hff.Field{schema.Bytes("asset")}
	for i := 0; i < 24; i++ {
		fields = append(fields, schema.String(fmt.Sprintf("text%d", i)))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(schema.Struct(fields...), schema.ListOf(schema.BytesType), schema.IntType)), schema.Any("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data[key]:type="bytes"`, `data:get(key!):type="bytes"`, `data:values:map(v=>v:type):json:exists`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, dynamicStructOptions(hffsql.JSON, false)...); err != nil {
			t.Fatal(source, err)
		}
		for _, after := range []int64{1, baseline.checks.Load() / 4, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
			ctx, cancel := context.WithCancel(context.Background())
			q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, dynamicStructOptions(hffsql.JSON, false)...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("partial %v, error %v", q, err)
			}
		}
	}
}

func structRootUnionSources() []string {
	sources := []string{
		`data:values:map(v=>v:type):json:exists`,
		`data:values:any(v=>v:type="bytes")`,
		`data:values:count(v=>v:type="bytes")=1`,
		`data:values:sum(v=>v:len)>0`,
		`data:values:avg(v=>v:len)>0`,
		`data:values:distinct:size>0`,
		`data:values:filter(v=>v:type="bytes"):map(v=>v:[bytes]:base64):json:exists`,
		`data:values:map(v=>[v]):flatmap(v=>v):any(v=>v:type="bytes")`,
		`data:values:find(v=>v:type="bytes"):type="bytes"`,
		`data:values:filter(v=>false):all(v=>true)`,
		`data:values[-1]:type="string"`,
		`data:values[0]:exists`,
		`data:values:json:exists`,
		`data:get(key):type="bytes"`,
		`data:get(key):type="null"`,
		`data:get(key!):type="null"`,
		`data:get(key:[string]):type="null"`,
		`data[key]:[bytes]:base64="YQ=="`,
		`data[key]:type="string"`,
		`data[key]:type="bytes"`,
		`data[key]:type="null"`,
		`data[key!]:type="null"`,
		`data![key!]:exists`,
		`data[key:[number]]:type="bytes"`,
		`[data][0][-1]:type="bytes"`,
		`[data]:any(row=>row[-1]:type="bytes")`,
		`["asset","text"]:map(key=>data:get(key):type):json:exists`,
		`["asset"]:any(key=>["text"]:any(key=>data[key]:type="string") data[key]:type="bytes")`,
	}
	for _, source := range []string{`data:values:any(v=>v:type="bytes")`, `data:get(key!):type="null"`, `data[key!]:type="null"`, `data[key]:type="bytes"`} {
		sources = append(sources, strings.ReplaceAll(source, "data", `[data][0]`))
		sources = append(sources, `[data]:any(row=>`+strings.ReplaceAll(source, "data", "row")+`)`)
	}
	return sources
}

func TestStructRootUnionUnobservedFloat(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(schema.Struct(schema.Bytes("asset"), schema.String("text")), schema.ListOf(schema.FloatType))), schema.Nullable(schema.String("key")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data[key]:json:exists`, `data[key!]:json:exists`, `data:get(key!):json:exists`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSONB, false)...); err != nil {
			t.Error(source, err)
		}
	}
}

func TestStructRootUnionValidateUnusedCallback(t *testing.T) {
	e, err := engine.New(engine.WithSchema(structRootUnionSchema(schema.AnyType)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `data:values:filter(v=>false):map(v=>v:string:concat("\u0000")):json:exists`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSON, false)...)
	requireCode(t, err, hff.BackendError)
}

func TestStructRootUnionBuild(t *testing.T) {
	for _, key := range []hff.Type{schema.AnyType, schema.StringType} {
		e, err := engine.New(engine.WithSchema(structRootUnionSchema(key)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range structRootUnionSources() {
			// Numeric-only narrowing is intentionally disjoint from String.
			if key.Kind() == hff.StringType && source == `data[key:[number]]:type="bytes"` {
				continue
			}
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, dynamicStructOptions(format, true)...); err != nil {
					t.Error(key, format, source, err)
				}
			}
		}
	}
}
