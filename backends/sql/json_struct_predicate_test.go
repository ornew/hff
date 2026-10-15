package sql_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func structPredicateSources() []string {
	const object = `{"asset":data.asset,"text":data.text,"rate":data.rate,"elapsed":data.elapsed,"recorded":data.recorded,"dynamic":data.dynamic}`
	var sources []string
	for _, receiver := range []string{"data", object} {
		selected := `["asset","text"]:map(key=>` + receiver + `[key])`
		sources = append(sources,
			selected+`:count(v=>v:len>0)=2`,
			selected+`:count(v=>v:type="bytes")=1`,
			selected+`:all(v=>v:exists)`,
			selected+`:any(v=>v:type="bytes")`,
			selected+`:filter(v=>false):count(v=>true)=0`,
			selected+`:map(v=>v!):filter(v=>false):count(v=>true)=0`,
			selected+`:count(v=>v:[string]~"[")=0`,
			selected+`:count(v=>["asset"]:any(key=>data:get(key):type="bytes") v:exists)>0`,
		)
		for _, access := range []string{`:get(key)`, `[key]`, `:get(key!)`} {
			wrapped := `[` + receiver + access + `]:flatmap(v=>[v])`
			sources = append(sources,
				wrapped+`:any(v=>v:type="bytes")`,
				wrapped+`:all(v=>v:exists)`,
				wrapped+`:count(v=>v:type="null")=1`,
				wrapped+`:map(v=>v!):filter(v=>false):count(v=>true)=0`,
			)
		}
	}
	return append(sources,
		`["asset","text"]:map(key=>data[key]):count(v=>["x"]:any(v=>v:type="string") v:exists)>0`,
		`[[data:get(key)]]:flatmap(xs=>xs):flatmap(v=>[v]):any(v=>v:type="bytes")`,
	)
}

func TestStructPredicateBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range structPredicateSources() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			if _, err := hffsql.Build(context.Background(), p, dynamicStructOptions(format, true)...); err != nil {
				t.Error(format, source, err)
			}
		}
	}
}

func TestStructPredicateCancellation(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 24; i++ {
		fields = append(fields, schema.Field(fmt.Sprintf("f%d", i), schema.Struct(schema.String(fmt.Sprintf("v%d", i)))))
	}
	fields = append(fields, schema.Field("asset", schema.Struct(schema.Bytes("v0"))))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(fields...))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `["asset","f0"]:map(key=>data[key]):count(v=>v:type="object")>0`)
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSON))}
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
			t.Fatalf("partial %v, error %v", q, err)
		}
	}
}

func TestStructPredicateValidateUnusedCallbacks(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`["asset"]:map(key=>data[key]):filter(v=>false):count(v=>v:string:concat("\u0000"))=0`,
		`[data:get(key!)]:flatmap(v=>[v]):filter(v=>false):any(v=>v:string:concat("\u0000"))`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSON, true)...)
		requireCode(t, err, hff.BackendError)
	}
}

func TestStructPredicateFloatProvenance(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(schema.Float("rate"), schema.String("text"), schema.Bytes("asset"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const selected = `["asset","rate"]:map(key=>data[key])`
	for _, tc := range []struct {
		suffix        string
		needsMetadata bool
	}{
		{`:count(v=>v:type="number")>0`, false},
		{`:any(v=>v:[float]>0)`, false},
		{`:count(v=>v:json:len>0)>0`, true},
	} {
		p, err := e.Compile(context.Background(), selected+tc.suffix)
		if err != nil {
			t.Fatal(err)
		}
		for _, metadata := range []bool{false, true} {
			var settings []hffsql.JSONBindingOption
			if metadata {
				settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("metadata")))
			}
			_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB, settings...)))
			if tc.needsMetadata && !metadata {
				requireCode(t, err, hff.BackendError)
			} else if err != nil {
				t.Error(tc.suffix, metadata, err)
			}
		}
	}
}

func TestStructPredicateRejectsAmbiguousStorage(t *testing.T) {
	for _, typ := range []hff.Type{schema.MapOf(schema.Union(schema.StringType, schema.BytesType)), schema.Struct(schema.Field("bad", schema.Union(schema.StringType, schema.BytesType)), schema.String("text")), schema.Union(schema.Struct(schema.String("value")), schema.Struct(schema.Bytes("value")))} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(context.Background(), `[data:get(key)]:flatmap(v=>[v]):any(v=>v:type="bytes")`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSON, true)...)
		requireCode(t, err, hff.BackendError)
	}
}
