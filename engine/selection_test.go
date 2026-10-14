package engine_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestSelectionResultTypesAndPermissions(t *testing.T) {
	typ := schema.Struct(schema.List("tags", schema.StringType))
	for _, tc := range []struct {
		source, id string
		result     hff.Type
	}{
		{`tags:distinct`, "list.distinct", schema.NullableType(schema.ListOf(schema.StringType))},
		{`tags:find(tag=>tag="api")`, "list.find", schema.NullableType(schema.StringType)},
	} {
		e := newEngine(t, typ)
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		call, _ := p.Root().Child(0)
		if !call.Type().Equal(tc.result) {
			t.Fatalf("%s type %s, want %s", tc.source, call.Type().Key(), tc.result.Key())
		}
		e = newEngine(t, typ, engine.WithCapabilities(capabilities.Standard().Without(capabilities.Method(tc.id))))
		_, err = e.Compile(context.Background(), tc.source)
		requireCode(t, err, hff.CapabilityError)
	}
}
