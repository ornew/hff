package sql

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPipelineStageRetainsInvariantWorkPlacement(t *testing.T) {
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.scope.v1", Name: "customscope", Receiver: schema.StringType, Result: schema.BoolType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("data", schema.AnyType), schema.String("outer"))), engine.WithMethods(declaration), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		gate bool
	}{
		{`x:[string]:len+1`, true},
		{`x:[string]:matches("^a")`, true},
		{`x:[list[string]]:map(y=>y:len):size`, true},
		{`x:[list[string]]:map(x=>x:len):size`, true},
		{`x:[list[string]]:any(y=>y:len>x:[list[string]]:size)`, true},
		{`outer:len+x:[string]:len`, false},
		{`x:[string]:len+"[]":fromjson:size`, false},
		{`x:[string]:len+[1,2]:map(y=>y+1):size`, false},
		{`x:[string]:customscope`, false},
		{`true`, false},
		{`x`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			p, err := e.Compile(context.Background(), `data:map(x=>`+tc.body+`):size>0`)
			if err != nil {
				t.Fatal(err)
			}
			var find func(hff.Node) hff.Node
			find = func(n hff.Node) hff.Node {
				if method, ok := n.Method(); ok && method.Name() == "map" {
					callback, _ := n.Child(1)
					return callback
				}
				for i := 0; i < n.ChildCount(); i++ {
					child, _ := n.Child(i)
					if callback := find(child); callback.Valid() {
						return callback
					}
				}
				return hff.Node{}
			}
			callback := find(p.Root())
			if !callback.Valid() {
				t.Fatal("missing map callback")
			}
			if got := rowLocalPipelineCallback(callback); got != tc.gate {
				t.Fatalf("gate=%v, want %v", got, tc.gate)
			}
		})
	}
}
