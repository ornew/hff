package sql

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestParsedValueCacheRetainsRepresentationAndPresence(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `data:fromjson.a:exists`)
	if err != nil {
		t.Fatal(err)
	}
	field, _ := p.Root().Child(0)
	b := &builder{ctx: context.Background(), program: p, jsonValues: map[string]JSONFormat{"decoded": JSON}}
	value := expression{"decoded", "first_presence", hff.AnyType}
	first, ok := b.shareableParsedValue(field, value)
	if !ok {
		t.Fatal("parsed field was not eligible")
	}
	for _, changed := range []expression{
		{"other_decoded", value.present, value.kind},
		{value.code, "other_presence", value.kind},
		{value.code, value.present, hff.StringType},
	} {
		if key, _ := b.shareableParsedValue(field, changed); key == first {
			t.Fatal("distinct decoded carrier reused the same key", changed)
		}
	}
	b.jsonValues[value.code] = JSONB
	if key, _ := b.shareableParsedValue(field, value); key == first {
		t.Fatal("JSON and JSONB reused the same key")
	}
	for _, source := range []string{`data:fromjson!.a:exists`, `data:fromjson:[object].a:exists`, `data:fromjson[0]:exists`, `({"a":data}).a:exists`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := p.Root().Child(0)
		if parsedJSONRoot(n) {
			t.Errorf("%s gained an unguarded parse reuse contract", source)
		}
	}
}
