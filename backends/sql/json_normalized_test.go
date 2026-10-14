package sql

import (
	"context"
	"testing"

	"github.com/ornew/hff"
)

func TestNormalizedJSONProofRequiresCompatibleRepresentation(t *testing.T) {
	integer := hff.PrimitiveType(hff.IntType)
	floating := hff.PrimitiveType(hff.FloatType)
	typ := hff.NewListType(integer)
	for _, tc := range []struct {
		name, code string
		format     JSONFormat
		typ        hff.Type
		reuse      bool
	}{
		{"exact", "mapped.v", JSON, typ, true},
		{"different value", "other.v", JSON, typ, false},
		{"physical cast", "mapped.v", JSONB, typ, false},
		{"different subtype", "mapped.v", JSON, hff.NewListType(floating), false},
		{"widened subtype", "mapped.v", JSON, hff.NewListType(hff.NewUnionType(integer, floating)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &builder{ctx: context.Background()}
			b.rememberNormalizedJSON("mapped.v", JSON, typ)
			got, err := b.comparisonDocument(hff.Node{}, tc.code, tc.format, tc.typ)
			if err != nil {
				t.Fatal(err)
			}
			if tc.reuse {
				if got != "COALESCE(mapped.v,'null'::json)" || len(b.steps) != 0 {
					t.Fatalf("unexpected proof reuse: %s", got)
				}
			} else {
				plain := &builder{ctx: context.Background()}
				want, err := plain.comparisonDocument(hff.Node{}, tc.code, tc.format, tc.typ)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("fallback differs from ordinary normalization: %s", got)
				}
			}
		})
	}
	b := &builder{ctx: context.Background()}
	b.rememberNormalizedJSON("raw.v", JSONB, typ)
	if b.isNormalizedJSON("raw.v", JSONB, typ) || b.isNormalizedJSON("raw.v", JSON, typ) {
		t.Fatal("JSONB producer received a JSON proof")
	}
}

func TestNormalizedJSONProofSurvivesOnlyValuePreservingAliases(t *testing.T) {
	integer := hff.PrimitiveType(hff.IntType)
	typ := hff.NewListType(integer)
	b := &builder{ctx: context.Background(), jsonValues: map[string]JSONFormat{}}
	b.rememberNormalizedJSON("mapped.v", JSON, typ)
	projected := b.project("mapped.v")
	decoded, err := b.jsonDecodeProjected(hff.Node{}, projected, JSON, typ, "guard_presence")
	if err != nil {
		t.Fatal(err)
	}
	if !b.isNormalizedJSON(decoded.code, JSON, typ) || decoded.present != "guard_presence" {
		t.Fatal("ordinary alias lost proof or presence")
	}
	for _, code := range []string{projected + "::jsonb", projected + " -> 0", projected + " -> 'field'", "unknown.v"} {
		if b.isNormalizedJSON(code, JSON, typ) {
			t.Errorf("derived value inherited proof: %s", code)
		}
	}
	// A different requested decoder type must not overwrite the producer's proof.
	other := hff.NewListType(hff.PrimitiveType(hff.FloatType))
	decoded, err = b.jsonDecodeProjected(hff.Node{}, projected, JSON, other, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	if b.isNormalizedJSON(decoded.code, JSON, other) {
		t.Fatal("decoding relabeled the producer's stored type")
	}
}

func TestNormalizedJSONProofDoesNotBypassSchemaOrCancellation(t *testing.T) {
	b := &builder{ctx: context.Background()}
	ambiguous := hff.NewListType(hff.NewUnionType(hff.PrimitiveType(hff.StringType), hff.PrimitiveType(hff.BytesType)))
	b.rememberNormalizedJSON("mapped.v", JSON, ambiguous)
	if _, err := b.comparisonDocument(hff.Node{}, "mapped.v", JSON, ambiguous); err == nil {
		t.Fatal("proof bypassed tagged-string ambiguity")
	}
	invalidKey := hff.NewListType(hff.NewStructType(hff.Field{Name: "bad\x00key", Type: hff.PrimitiveType(hff.IntType)}))
	b.rememberNormalizedJSON("mapped.v", JSON, invalidKey)
	if _, err := b.comparisonDocument(hff.Node{}, "mapped.v", JSON, invalidKey); err == nil {
		t.Fatal("proof bypassed PostgreSQL NUL key restriction")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ctx = ctx
	typ := hff.NewListType(hff.PrimitiveType(hff.IntType))
	b.rememberNormalizedJSON("mapped.v", JSON, typ)
	if _, err := b.comparisonDocument(hff.Node{}, "mapped.v", JSON, typ); err == nil {
		t.Fatal("proof bypassed cancellation")
	}
}

func TestNormalizationOutputProofDeclinesUnsupportedSchema(t *testing.T) {
	b := &builder{ctx: context.Background()}
	typ := hff.NewUnionType(hff.PrimitiveType(hff.StringType), hff.PrimitiveType(hff.BytesType))
	if allowed, err := b.normalizationProofSchema(hff.Node{}, typ); err != nil || allowed {
		t.Fatalf("optional unsupported proof: %t %v", allowed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ctx = ctx
	if _, err := b.normalizationProofSchema(hff.Node{}, hff.PrimitiveType(hff.IntType)); err == nil {
		t.Fatal("optional proof swallowed cancellation")
	}
}
