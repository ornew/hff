package sql

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func TestComputedJSONDocumentReusesNativeScalar(t *testing.T) {
	for _, kind := range []hff.TypeKind{hff.BoolType, hff.IntType, hff.FloatType, hff.StringType, hff.BytesType, hff.DurationType, hff.TimestampType} {
		for _, nullable := range []bool{false, true} {
			typ := hff.PrimitiveType(kind)
			if nullable {
				typ = hff.NewUnionType(typ, hff.PrimitiveType(hff.NullType))
			}
			b := &builder{ctx: context.Background(), jsonValues: map[string]JSONFormat{}}
			value := expression{"native_value", "guard_presence", kind}
			raw, _, err := b.jsonRepresentation(hff.Node{}, value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.computedJSONDocument(hff.Node{}, value, typ)
			if err != nil {
				t.Fatal(err)
			}
			if want := "COALESCE(" + raw + ",'null'::json)"; got != want {
				t.Errorf("%s: got %s, want %s", typ.Key(), got, want)
			}
			if len(b.steps) != 0 || strings.Contains(got, "guard_presence") {
				t.Errorf("%s moved projections or presence into the JSON carrier", typ.Key())
			}
		}
	}
}

func TestComputedJSONDocumentRetainsNormalization(t *testing.T) {
	integer := hff.PrimitiveType(hff.IntType)
	float := hff.PrimitiveType(hff.FloatType)
	for _, tc := range []struct {
		name   string
		value  expression
		stored hff.Type
		json   bool
	}{
		{"JSON scalar", expression{"encoded", "TRUE", hff.IntType}, integer, true},
		{"wider schema", expression{"native_value", "TRUE", hff.IntType}, hff.NewUnionType(integer, float), false},
		{"Any schema", expression{"native_value", "TRUE", hff.IntType}, hff.PrimitiveType(hff.AnyType), false},
		{"container", expression{"encoded", "TRUE", hff.ListType}, hff.NewListType(integer), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &builder{ctx: context.Background(), jsonValues: map[string]JSONFormat{}}
			if tc.json {
				b.jsonValues[tc.value.code] = JSON
			}
			raw, format, err := b.jsonRepresentation(hff.Node{}, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			want, err := b.comparisonDocument(hff.Node{}, raw, format, tc.stored)
			if err != nil {
				t.Fatal(err)
			}
			// Normalization can allocate aliases, so compare fresh builders.
			b.alias = 0
			got, err := b.computedJSONDocument(hff.Node{}, tc.value, tc.stored)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("got %s, want ordinary normalization %s", got, want)
			}
		})
	}
}

func TestComputedJSONDocumentChecksSchemaAndCancellation(t *testing.T) {
	stringType := hff.PrimitiveType(hff.StringType)
	bytesType := hff.PrimitiveType(hff.BytesType)
	b := &builder{ctx: context.Background(), jsonValues: map[string]JSONFormat{}}
	if _, err := b.computedJSONDocument(hff.Node{}, expression{"v", "TRUE", hff.StringType}, hff.NewUnionType(stringType, bytesType)); err == nil {
		t.Fatal("ambiguous stored string families were accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ctx = ctx
	if _, err := b.computedJSONDocument(hff.Node{}, expression{"v", "TRUE", hff.IntType}, hff.PrimitiveType(hff.IntType)); err == nil {
		t.Fatal("native scalar bypass ignored cancellation")
	}
}
