package sql

import (
	"context"
	"reflect"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestNormalizedFoldProofRestoresEnclosingScope(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("items", schema.ListOf(schema.NullableType(schema.FloatType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:foldl([], (a,x)=>a:concat([x])):exists`)
	if err != nil {
		t.Fatal(err)
	}
	fold, _ := p.Root().Child(0)
	receiver, _ := fold.Child(0)
	seed, _ := fold.Child(1)
	callback, _ := fold.Child(2)
	parameters, _ := callback.Parameters()
	outer := jsonParameter{code: "outer.acc", format: JSON, typ: schema.StringType, lossy: true}
	b := &builder{ctx: ctx, program: p, parameters: map[hff.BindingID]jsonParameter{parameters[0].ID(): outer}, jsonValues: map[string]JSONFormat{}, steps: []string{"outer_step"}}
	b.rememberNormalizedJSON("previous.acc", JSON, schema.StringType)
	prior := b.normalizedJSONValues["previous.acc"]
	result, steps, _, err := b.jsonFoldBody(callback, parameters, 0, "previous", "item", parameters[0].Type(), b.jsonStoredType(seed), receiver, seed, JSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 || result.code == "" {
		t.Fatal("callback was not lowered")
	}
	if !reflect.DeepEqual(b.parameters[parameters[0].ID()], outer) || len(b.parameters) != 1 {
		t.Fatal("callback replaced enclosing parameters")
	}
	if got := b.normalizedJSONValues["previous.acc"]; !reflect.DeepEqual(got, prior) {
		t.Fatal("inductive proof replaced enclosing provenance")
	}
	if !reflect.DeepEqual(b.steps, []string{"outer_step"}) {
		t.Fatal("callback projections escaped their scope")
	}
	delete(b.normalizedJSONValues, "previous.acc")
	if _, _, _, err := b.jsonFoldBody(callback, parameters, 0, "previous", "item", parameters[0].Type(), b.jsonStoredType(seed), receiver, seed, JSON); err != nil {
		t.Fatal(err)
	}
	if _, exists := b.normalizedJSONValues["previous.acc"]; exists {
		t.Fatal("temporary predecessor proof escaped callback scope")
	}
}
