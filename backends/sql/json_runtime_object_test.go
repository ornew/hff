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

func runtimeObjectSources() []string {
	var sources []string
	for _, fold := range []string{`data:values:foldl({"v":null},(a,v)=>{"v":v})`, `data:values:foldr({"v":null},(v,a)=>{"v":v})`} {
		for _, suffix := range []string{
			`:keys:json:exists`, `:keys:map(k=>k:upper):json:exists`,
			`:values:map(v=>v:type):json:exists`, `:values:any(v=>v:type="bytes")`,
			`:values:distinct:map(v=>v:type):json:exists`,
			`:values:find(v=>true):type="bytes"`,
			`:values:foldl([],(a,v)=>a:concat([v])):map(v=>v:type):json:exists`,
			`:items:map(p=>p[0]):json:exists`, `:items:map(p=>p[1]:type):json:exists`,
			`:items:distinct:map(p=>p[0]:json):json:exists`,
			`:items:find(p=>true)[0]:json:exists`,
			`:items:map(p=>p):concat([]):map(p=>p[0]:json):json:exists`,
			`:items:map(p=>p:[list[any]][0]:json):json:exists`,
			`:items:foldl([],(a,p)=>a:concat([p])):map(p=>p[0]:json):json:exists`,
			`:items:any(p=>p[0]="v" p[1]:type="bytes")`,
			`:items:distinct:map(p=>p[1]:type):json:exists`,
			`:items:find(p=>true)[1]:type="bytes"`,
			`:items:foldl([],(a,p)=>a:concat([p])):map(p=>p[1]:type):json:exists`,
			`:items:filter(p=>p[0]="v"):map(p=>p[1]):map(v=>v:type):json:exists`,
			`:values:filter(v=>false):all(v=>true)`,
		} {
			sources = append(sources, fold+suffix)
		}
	}
	const folded = `data:values:foldl({"v":null},(a,v)=>{"v":v})`
	sources = append(sources,
		`{"boxed":`+folded+`}:items:map(p=>p[1]:keys:json):json:exists`,
		`{"boxed":`+folded+`}:keys:json:exists`,
		`[`+folded+`]:find(obj=>true):items:map(p=>p[1]:type):json:exists`,
	)
	const ordered = `data:values:foldl({"z":null,"":0,"é":false,"中":null,"a":""},(a,v)=>{"z":v,"":0,"é":true,"中":v,"a":"A"})`
	for _, suffix := range []string{
		`:keys:json=["","a","z","é","中"]:json`,
		`:items:map(p=>p[0]):json=["","a","z","é","中"]:json`,
		`:values[0]=0`, `:values[1]="A"`, `:values[3]=true`,
		`:items[2][0]="z"`, `:items[4][0]="中"`,
		`:items[2][1]:type=data:values[-1]:type`,
	} {
		sources = append(sources, ordered+suffix)
	}
	return append(sources,
		`data:items:map(p=>p[1]):foldl({"v":null},(a,v)=>{"v":v}):values:map(v=>v:type):json:exists`,
		`data:values:foldl({},(a,v)=>{"v":v}):keys:json:exists`,
		`data:values:foldl({"v":null},(a,v)=>{"v":v!}):keys:type="null"`,
		`data:values:foldl({"v":null},(a,v)=>{"v":v!}):values:exists`,
		`data:values:foldl({"v":null},(a,v)=>{"v":v!}):items:map(p=>p[0]):json:exists`,
		`[data:values:foldl({"v":null},(a,v)=>{"v":v})]:any(obj=>obj:items:any(p=>["v"]:any(p=>p="v") p[1]:type="bytes"))`,
	)
}

func runtimeObjectKeySources() []string {
	const fold = `data:values:foldl({"v":null},(a,v)=>{"v":v})`
	return []string{
		fold + `:keys:json:exists`,
		fold + `:items:map(p=>p[0]:json):json:exists`,
		fold + `:items:distinct:map(p=>p[0]:json):json:exists`,
		fold + `:items:find(p=>true)[0]:json:exists`,
		fold + `:items:map(p=>p):concat([]):map(p=>p[0]:json):json:exists`,
		fold + `:items:map(p=>p:[list[any]][0]:json):json:exists`,
		fold + `:items:foldl([],(a,p)=>a:concat([p])):map(p=>p[0]:json):json:exists`,
		fold + `:items:map(p=>[p]:foldl([],(a,q)=>a:concat([q]))):flatmap(xs=>xs):map(p=>p[0]:json):json:exists`,
	}
}

func TestRuntimeObjectEnumerationKeyProvenance(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Struct(schema.Bytes("asset"), schema.Float("rate"), schema.String("text"))), schema.Any("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range runtimeObjectKeySources() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSONB, false)...); err != nil {
			t.Error(source, err)
		}
	}
	// Replacing pair slots must discard the key fact: slot zero can now be
	// a Float whose signed zero was not preserved by the stored JSONB.
	source := `data:values:foldl({"v":null},(a,v)=>{"v":v}):items:foldl([],(a,p)=>a:concat([[p[1],p[0]]])):map(p=>p[0]:json):json:exists`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSONB, false)...)
	requireCode(t, err, hff.BackendError)
}

func TestRuntimeObjectEnumerationValidation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`data:values:foldl({"v":null},(a,v)=>{"v":v}):values:filter(v=>false):map(v=>v:string:concat("\u0000")):json:exists`,
		`data:values:foldl({"v":null},(a,v)=>{"v":v}):items:filter(p=>false):map(p=>p[0]:concat("\u0000")):json:exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, dynamicStructOptions(hffsql.JSON, false)...)
		requireCode(t, err, hff.BackendError)
	}
	p, err := e.Compile(context.Background(), `data:values:foldl({"v":null},(a,v)=>{"v":v}):items:map(p=>p[1]:type):json:exists`)
	if err != nil {
		t.Fatal(err)
	}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, dynamicStructOptions(hffsql.JSON, false)...); err != nil {
		t.Fatal(err)
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

func TestRuntimeObjectEnumerationBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range runtimeObjectSources() {
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
