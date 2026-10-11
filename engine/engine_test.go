package engine_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func newEngine(t *testing.T, typ hff.Type, options ...engine.Option) *engine.Engine {
	t.Helper()
	base := []engine.Option{engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard())}
	e, err := engine.New(append(base, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func requireCode(t *testing.T, err error, code hff.ErrorCode) *hff.Error {
	t.Helper()
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != code {
		t.Fatalf("got %v; want code %s", err, code)
	}
	return detail
}
func TestPresenceAndInputValidation(t *testing.T) {
	cases := []struct {
		name   string
		typ    hff.Type
		record hff.Value
		valid  bool
	}{
		{"required missing", schema.Struct(schema.String("name")), hff.Object(nil), false},
		{"required any missing", schema.Struct(schema.Any("name")), hff.Object(nil), false},
		{"optional absent", schema.Struct(schema.Optional(schema.String("name"))), hff.Object(nil), true},
		{"optional explicit null", schema.Struct(schema.Optional(schema.String("name"))), hff.Object(map[string]hff.Value{"name": hff.Null()}), false},
		{"nullable null", schema.Struct(schema.Nullable(schema.String("name"))), hff.Object(map[string]hff.Value{"name": hff.Null()}), true},
		{"nullable absent", schema.Struct(schema.Nullable(schema.String("name"))), hff.Object(nil), true},
		{"unexpected field", schema.Struct(), hff.Object(map[string]hff.Value{"extra": hff.Int(1)}), false},
		{"dynamic map", schema.MapOf(schema.IntType), hff.Object(map[string]hff.Value{"extra": hff.Int(1)}), true},
		{"int not float", schema.Struct(schema.Float("x")), hff.Object(map[string]hff.Value{"x": hff.Int(1)}), false},
		{"union int", schema.Struct(schema.Field("x", schema.Union(schema.FloatType, schema.IntType))), hff.Object(map[string]hff.Value{"x": hff.Int(1)}), true},
		{"invalid any", schema.Struct(schema.Any("x")), hff.Object(map[string]hff.Value{"x": hff.List(hff.Value{})}), false},
		{"nan any", schema.Struct(schema.Any("x")), hff.Object(map[string]hff.Value{"x": hff.Float(math.NaN())}), false},
		{"infinite", schema.Struct(schema.Float("x")), hff.Object(map[string]hff.Value{"x": hff.Float(math.Inf(1))}), false},
		{"invalid utf8", schema.Struct(schema.String("x")), hff.Object(map[string]hff.Value{"x": hff.String(string([]byte{0xff}))}), false},
		{"invalid key", schema.MapOf(schema.AnyType), hff.Object(map[string]hff.Value{string([]byte{0xff}): hff.Null()}), false},
		{"timestamp range", schema.Struct(schema.Timestamp("x")), hff.Object(map[string]hff.Value{"x": hff.Timestamp(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))}), false},
		{"empty never list", schema.Struct(schema.List("x", schema.NeverType)), hff.Object(map[string]hff.Value{"x": hff.List()}), true},
		{"nonempty never list", schema.Struct(schema.List("x", schema.NeverType)), hff.Object(map[string]hff.Value{"x": hff.List(hff.Null())}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, tc.typ)
			input, err := e.Validate(context.Background(), tc.record)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if !input.Compatible(tc.typ) {
					t.Fatal("input not compatible")
				}
				_, exists := input.Value().Lookup("name")
				_, wasPresent := tc.record.Lookup("name")
				if exists != wasPresent {
					t.Fatal("validation changed presence")
				}
			} else {
				requireCode(t, err, hff.InputError)
			}
		})
	}
}
func TestConfigurationAndOwnership(t *testing.T) {
	base := &engine.Config{Schema: schema.Struct(schema.String("name")), Capabilities: capabilities.Standard()}
	e, err := engine.New(engine.WithConfig(base), engine.WithSchema(schema.Struct(schema.Int("age"))))
	if err != nil {
		t.Fatal(err)
	}
	base.Schema = schema.Struct()
	base.Capabilities = capabilities.None()
	if !e.Schema().Equal(schema.Struct(schema.Int("age"))) {
		t.Fatal("config retained mutable pointer or options applied out of order")
	}
	e2, err := engine.New(engine.WithSchema(schema.Struct()), engine.WithConfig(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(e2.Capabilities().IDs()) != 0 {
		t.Fatal("WithConfig did not replace previous settings")
	}
	for _, options := range [][]engine.Option{
		{engine.WithSchema(schema.Struct())},
		{engine.WithConfig(nil)},
		{engine.Option{}},
		{engine.WithSchema(schema.Struct(schema.String("x"), schema.Int("x"))), engine.WithCapabilities(capabilities.Standard())},
		{engine.WithSchema(schema.StringType), engine.WithCapabilities(capabilities.Standard())},
		{engine.WithSchema(schema.Struct()), engine.WithCapabilities(capabilities.None().Allow("unknown"))},
		{engine.WithSchema(schema.Struct()), engine.WithCapabilities(capabilities.Standard().Without(capabilities.Callbacks))},
		{engine.WithSchema(schema.Struct()), engine.WithCapabilities(capabilities.Standard()), engine.WithLimits(hff.Limits{MaxSteps: -1})},
	} {
		_, err := engine.New(options...)
		requireCode(t, err, hff.ConfigurationError)
	}
}
func TestStructuralInputCompatibility(t *testing.T) {
	a := schema.Struct(schema.String("name"), schema.Field("age", schema.Union(schema.IntType, schema.FloatType)))
	b := schema.Struct(schema.Field("age", schema.Union(schema.FloatType, schema.IntType, schema.IntType)), schema.String("name"))
	e := newEngine(t, a)
	other := newEngine(t, b, engine.WithCapabilities(capabilities.None()))
	input, err := e.Validate(context.Background(), hff.Object(map[string]hff.Value{"name": hff.String("Alice"), "age": hff.Int(18)}))
	if err != nil {
		t.Fatal(err)
	}
	if !input.Compatible(other.Schema()) {
		t.Fatal("identical reordered schema rejected")
	}
	for _, different := range []hff.Type{
		schema.Struct(schema.String("name"), schema.Int("age")),
		schema.Struct(schema.Optional(schema.String("name")), schema.Field("age", schema.Union(schema.IntType, schema.FloatType))),
		schema.Struct(schema.Nullable(schema.String("name")), schema.Field("age", schema.Union(schema.IntType, schema.FloatType))),
	} {
		if input.Compatible(different) {
			t.Fatal("different contract accepted")
		}
	}
	if (hff.Input{}).Compatible(a) {
		t.Fatal("zero input accepted")
	}
	// A key containing a dot is not a nested path.
	dotted := schema.Struct(schema.String("a.b"))
	nested := schema.Struct(schema.Field("a", schema.Struct(schema.String("b"))))
	if dotted.Equal(nested) {
		t.Fatal("literal and nested names conflated")
	}
}
func TestLimitsAndCancellation(t *testing.T) {
	typ := schema.Struct(schema.Any("x"))
	record := hff.Object(map[string]hff.Value{"x": hff.List(hff.Int(1), hff.Int(2))})
	small := newEngine(t, typ, engine.WithLimits(hff.Limits{MaxInputValues: 3}))
	_, err := small.Validate(context.Background(), record)
	requireCode(t, err, hff.LimitError)
	small = newEngine(t, typ, engine.WithLimits(hff.Limits{MaxInputDepth: 1}))
	_, err = small.Validate(context.Background(), record)
	requireCode(t, err, hff.LimitError)
	unlimited := newEngine(t, typ, engine.WithLimits(hff.Limits{MaxInputValues: 1}), engine.WithUnlimitedLimits())
	if _, err = unlimited.Validate(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	restored := newEngine(t, typ, engine.WithUnlimitedLimits(), engine.WithLimits(hff.Limits{}))
	if restored.Limits() != hff.DefaultLimits() {
		t.Fatal("zero did not restore defaults")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = restored.Validate(ctx, record)
	requireCode(t, err, hff.CancelledError)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("context cause lost")
	}
	_, err = restored.Validate(nil, record)
	requireCode(t, err, hff.ConfigurationError)
}
func TestImmutableValuesAndConcurrentValidation(t *testing.T) {
	bytes := []byte{1, 2}
	elements := []hff.Value{hff.Bytes(bytes)}
	fields := map[string]hff.Value{"x": hff.List(elements...)}
	record := hff.Object(fields)
	bytes[0] = 9
	elements[0] = hff.Null()
	fields["x"] = hff.Null()
	e := newEngine(t, schema.Struct(schema.List("x", schema.BytesType)))
	input, err := e.Validate(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	obj, _ := input.Value().AsObject()
	list, _ := obj["x"].AsList()
	buffer, _ := list[0].AsBytes()
	buffer[0] = 7
	list[0] = hff.Null()
	obj["x"] = hff.Null()
	x, _ := input.Value().Lookup("x")
	saved, _ := x.AsList()
	savedBytes, _ := saved[0].AsBytes()
	if savedBytes[0] != 1 {
		t.Fatal("mutable value storage exposed")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if _, err := e.Validate(context.Background(), record); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
func BenchmarkValidateRecord(b *testing.B) {
	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				b.Fatal(err)
			}
			items := make([]hff.Value, count)
			for i := range items {
				items[i] = hff.Int(int64(i))
			}
			record := hff.Object(map[string]hff.Value{"items": hff.List(items...)})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := e.Validate(context.Background(), record); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func ExampleEngine_Validate() {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"), schema.Int("age"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	input, err := e.Validate(context.Background(), hff.Object(map[string]hff.Value{"name": hff.String("Alice"), "age": hff.Int(18)}))
	if err != nil {
		panic(err)
	}
	name, _ := input.Value().Lookup("name")
	text, _ := name.AsString()
	fmt.Println(text, input.Compatible(schema.Struct(schema.Int("age"), schema.String("name"))))
	// Output: Alice true
}

// Ensure arbitrarily long schema names do not collide in canonical keys.
func TestSchemaNames(t *testing.T) {
	names := []string{"", "a.b", "a:b;true", "null", "日本語", "line\nbreak", strings.Repeat("x", 100)}
	for _, name := range names {
		typ := schema.Struct(schema.String(name))
		e := newEngine(t, typ)
		if _, err := e.Validate(context.Background(), hff.Object(map[string]hff.Value{name: hff.String("ok")})); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiagnosticPathsOwnTheirStorage(t *testing.T) {
	e := newEngine(t, schema.Struct(schema.List("items", schema.Struct(schema.Int("age")))))
	record := hff.Object(map[string]hff.Value{"items": hff.List(hff.Object(map[string]hff.Value{"age": hff.Int(18)}), hff.Object(map[string]hff.Value{"age": hff.String("wrong")}))})
	_, err := e.Validate(context.Background(), record)
	detail := requireCode(t, err, hff.InputError)
	if fmt.Sprint(detail.Path) != "[items 1 age]" {
		t.Fatalf("wrong path: %v", detail.Path)
	}
	_, _ = e.Validate(context.Background(), hff.Object(nil))
	if fmt.Sprint(detail.Path) != "[items 1 age]" {
		t.Fatal("later validation mutated saved diagnostic")
	}
}
