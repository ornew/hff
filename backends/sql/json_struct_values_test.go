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

func structValuesSources() []string {
	const object = `{"asset":data.asset,"text":data.text,"rate":data.rate,"elapsed":data.elapsed,"recorded":data.recorded,"dynamic":data.dynamic}`
	var sources []string
	for _, receiver := range []string{"data", object} {
		value := receiver + `:values`
		for _, suffix := range []string{
			`:any(v=>v:type="bytes")`, `:all(v=>v:exists)`, `:count(v=>v:type="bytes")=1`,
			`:size>0`, `:count>0`, `[0]:type="bytes"`, `[-1]:type="string"`,
			`:json:exists`, `:string:exists`, `:map(v=>v:type):json:exists`,
			`:map(v=>v):map(v=>v:type):json:exists`,
			`:filter(v=>v:type="bytes"):map(v=>v:[bytes]:base64):json:exists`,
			`:find(v=>v:type="bytes"):type="bytes"`, `:distinct:size>0`,
			`:sum(v=>v:len)>0`, `:foldl(0,(acc,v)=>acc+v:len)>0`,
			`:map(v=>[v]):flatmap(v=>v):map(v=>v:type):json:exists`,
			`:filter(v=>false):all(v=>true)`,
			`:map(v=>v!):filter(v=>false):all(v=>true)`,
			`:any(v=>["x"]:any(v=>v:type="string") v:exists)`,
			`:any(v=>["asset"]:any(key=>data:get(key):type="bytes") v:exists)`,
		} {
			sources = append(sources, value+suffix)
		}
	}
	return append(sources,
		`{"asset":data.asset!,"text":data.text}:values:filter(v=>false):all(v=>true)`,
		`[data][0]:values:any(v=>v:type="bytes")`,
		`[data]:find(v=>true):values:any(v=>v:type="bytes")`,
	)
}

func TestStructValuesBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range structValuesSources() {
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

func TestStructValuesCancellation(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 24; i++ {
		fields = append(fields, schema.Field(fmt.Sprintf("f%d", i), schema.Struct(schema.String(fmt.Sprintf("v%d", i)))))
	}
	fields = append(fields, schema.Field("asset", schema.Struct(schema.Bytes("v0"))))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(fields...))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `data:values:map(v=>v:type):json:exists`)
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

func TestStructValuesFloatProvenance(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(schema.Float("rate"), schema.String("text"), schema.Bytes("asset"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source        string
		needsMetadata bool
	}{
		{`data:values:any(v=>v:type="number")`, false},
		{`data:values:map(v=>v:type):json:exists`, false},
		{`data:values:json:exists`, true},
		{`data:values:map(v=>v):string:exists`, true},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
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
				t.Error(tc.source, metadata, err)
			}
		}
	}
}

func TestStructValuesValidateUnusedCallbacks(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:values:filter(v=>false):map(v=>v:string:concat("\u0000")):json:exists`, `{"asset":data.asset!,"text":data.text}:values:filter(v=>false):map(v=>v:string:concat("\u0000")):json:exists`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSON, true)...)
		requireCode(t, err, hff.BackendError)
	}
}
