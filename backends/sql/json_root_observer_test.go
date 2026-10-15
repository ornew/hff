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

func TestRootNumericGuardObserversDoNotRebuildPayload(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`data:[list[float]]:exists`, `data:[list[int]]:size=2`,
		`data:[map[float]]:type="object"`, `data:[map[list[float]]]:size=2`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, unionProjectionOptions(format, format == hffsql.JSONB)...)
			if err != nil {
				t.Fatal(format, source, err)
			}
			for _, rebuild := range []string{"json_agg(", "jsonb_agg(", "json_object_agg(", "jsonb_object_agg("} {
				if strings.Contains(q.Where(), rebuild) {
					t.Errorf("%s %s rebuilds a payload for a root-only observation", format, source)
				}
			}
		}
	}
}

func TestRootNumericGuardObserversPreserveValueConsumers(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`data:[list[float]]:exists data:[list[float]]:json:exists`,
		`data:[list[float]]:size=1 data:[list[float]]:json:exists`,
		`data:[list[float]]:[list[null]]:exists`,
		`data:[list[float]]:count(x=>x=null)>0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, unionProjectionOptions(hffsql.JSON, false)...)
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "json_agg(") {
			t.Errorf("%s discarded normalization required by a value consumer", source)
		}
	}
}

func rootObserverSources() []string {
	return []string{
		`data:type="object"`, `data:type="number"`,
		`data:[object].safe="ok"`, `data:[object]["safe"]="ok"`,
		`data:[map[any]]:get("safe")="ok"`, `data:[any]:type="object"`,
		`data:[null]=null`, `data:[string]:exists`, `data:[int]=0`,
		`data:[list[bytes]]:size=0`, `data:[list[any]]:count=0`,
		`data:[object|null]:type="null"`, `!(data:[object].safe=null)`,
		`data:[object].safe!:[string]:len>0`, `data:[object].nums:sum>0`,
		`data:[map[any]]:[object].nums:json="[1,2,3]"`,
		`[data:[object].safe]:json:exists`, `data:[object]:has("safe")`,
		`data:[list[bytes]]:map(x=>x:base64):json:exists`,
	}
}

func rootObserverCallbackSources() []string {
	return []string{
		`data:any(x=>x:type="object")`,
		`data:map(x=>x:type):json:exists`,
		`data:count(x=>x:[object].safe="ok")>0`,
		`data:any(x=>x:[map[any]]:get("safe")="ok")`,
		`data:map(x=>x:[object].nums):flatmap(xs=>xs):sum>0`,
		`data:all(x=>!(x:[object].safe=null))`,
		`data:count(x=>x:[null]=null)>0`,
		`data:count(x=>x:[int]=0)>0`,
		`data:any(x=>x:[list[bytes]]:map(v=>v:base64):json:exists)`,
		`data:filter(x=>x:[object].safe="ok"):map(x=>x:[map[any]].safe):json:exists`,
		`data:any(x=>["ok"]:any(y=>x:[object].safe=y))`,
	}
}

func TestRootObserversBuild(t *testing.T) {
	for _, tc := range unionProjectionTypes() {
		for _, callbacks := range []bool{false, true} {
			typ, sources := tc.typ, rootObserverSources()
			if callbacks {
				typ, sources = schema.ListOf(schema.Union(tc.typ, schema.NullType)), rootObserverCallbackSources()
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

func TestRootObserversPreserveAnyAlternatives(t *testing.T) {
	for _, tc := range []struct {
		typ      hff.Type
		positive string
		negative string
	}{
		{schema.Union(schema.AnyType, schema.Struct(schema.Bytes("asset"))), `data:[object]:exists`, `data:[map[string]]:exists`},
		{schema.Union(schema.AnyType, schema.ListOf(schema.BytesType)), `data:[list[any]]:exists`, `data:[list[string]]:exists`},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{tc.positive, tc.negative} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, unionProjectionOptions(format, format == hffsql.JSONB)...)
				if source == tc.negative {
					requireCode(t, err, hff.BackendError)
				} else if err != nil {
					t.Error(source, format, err)
				}
			}
		}
	}
}

func TestRootObserversPreserveConsumedAmbiguity(t *testing.T) {
	for _, tc := range unionProjectionTypes() {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", tc.typ)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{
			`data:[object].bad:type="bytes"`, `data:[map[any]]:get("bad"):exists`,
			`data:[object]:json:exists`, `data:[object]=data:[object]`,
			`data:[object].safe="ok" data:[object]:json:exists`,
			`data:[map[string]]:exists`,
		} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, unionProjectionOptions(format, format == hffsql.JSONB)...)
				requireCode(t, err, hff.BackendError)
			}
		}
	}
}

func TestRootObserversShapeOnlyJSONB(t *testing.T) {
	for _, typ := range []hff.Type{schema.AnyType, schema.Union(schema.MapOf(schema.AnyType), schema.ListOf(schema.FloatType))} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			source        string
			needsMetadata bool
		}{
			{`data:[object]:exists`, false},
			{`data:[list[any]]:count>0`, false},
			{`data:[object]:get("n"):[float]:exists`, true},
			{`data:[object|list[float]]:get("n"):[int]:exists`, true},
		} {
			p, err := e.Compile(context.Background(), tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			for _, metadata := range []bool{false, true} {
				_, err := hffsql.Build(context.Background(), p, unionProjectionOptions(hffsql.JSONB, metadata)...)
				if tc.needsMetadata && !metadata {
					requireCode(t, err, hff.BackendError)
				} else if err != nil {
					t.Error(typ.Key(), tc.source, metadata, err)
				}
			}
		}
	}
}

func TestRootObserversCancellation(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 30; i++ {
		fields = append(fields, schema.Optional(schema.Nullable(schema.Int(fmt.Sprintf("f%d", i)))))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(schema.Struct(fields...), schema.ListOf(schema.AnyType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:type="object"`, `data:[object|list[float]]:exists`, `data:[map[float]|list[any]]:exists`, `data:[map[float]]:size=0`, `data:[list[float]]:exists`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		options := unionProjectionOptions(hffsql.JSON, false)
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, options...); err != nil {
			t.Fatal(source, err)
		}
		for _, after := range []int64{1, baseline.checks.Load() / 4, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
			ctx, cancel := context.WithCancel(context.Background())
			q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, options...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("%s partial %v, error %v", source, q, err)
			}
		}
	}
}

func TestRootObserversValidateUnusedCallbacks(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.ListOf(unionProjectionTypes()[1].typ)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`data:filter(x=>false):map(x=>x:[object].safe:concat("\u0000")):json:exists`,
		`data:filter(x=>false):map(x=>x:[map[any]]:get("safe")):any(v=>v:concat("\u0000"))`,
		`data:filter(x=>false):map(x=>x:string:concat("\u0000")):[list[float]]:size=0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, unionProjectionOptions(hffsql.JSON, false)...)
		requireCode(t, err, hff.BackendError)
	}
}

func TestRootObserversDeclaredFloatJSONB(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		element := schema.FloatType
		if mixed {
			element = schema.Union(schema.IntType, schema.FloatType)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(schema.ListOf(element), schema.IntType)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(context.Background(), `data:[list[float]]:exists`)
		if err != nil {
			t.Fatal(err)
		}
		for _, metadata := range []bool{false, true} {
			_, err := hffsql.Build(context.Background(), p, unionProjectionOptions(hffsql.JSONB, metadata)...)
			if mixed && !metadata {
				requireCode(t, err, hff.BackendError)
			} else if err != nil {
				t.Error(mixed, metadata, err)
			}
		}
	}
}
