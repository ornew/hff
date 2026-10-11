package govalue

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func convert(t *testing.T, source any, fields Fields) hff.Value {
	t.Helper()
	v, err := Convert(context.Background(), source, fields, hff.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRepresentationAndOwnership(t *testing.T) {
	bytes := []byte{0, 255}
	stamp := time.Date(2026, 10, 12, 1, 2, 3, 4, time.UTC)
	source := map[string]any{
		"large": int64(9007199254740993), "unsigned": uint64(math.MaxInt64),
		"float": 1.0, "negativeZero": math.Copysign(0, -1),
		"bytes": bytes, "time": stamp, "duration": time.Nanosecond,
		"null": nil, "empty": []any{}, "list": []any{true, "a", int32(2)},
	}
	v := convert(t, source, nil)
	get := func(name string) hff.Value { x, _ := v.Lookup(name); return x }
	if x, ok := get("large").AsInt(); !ok || x != 9007199254740993 {
		t.Fatal("integer precision lost")
	}
	if x, ok := get("unsigned").AsInt(); !ok || x != math.MaxInt64 {
		t.Fatal("unsigned integer precision lost")
	}
	if _, ok := get("float").AsFloat(); !ok {
		t.Fatal("integral float converted to int")
	}
	if x, ok := get("negativeZero").AsFloat(); !ok || !math.Signbit(x) {
		t.Fatal("negative zero lost")
	}
	if x, ok := get("time").AsTimestamp(); !ok || !x.Equal(stamp) {
		t.Fatal("timestamp lost")
	}
	if x, ok := get("duration").AsDuration(); !ok || x != time.Nanosecond {
		t.Fatal("duration lost")
	}
	if get("null").Kind() != hff.NullKind {
		t.Fatal("present null lost")
	}
	if _, exists := v.Lookup("missing"); exists {
		t.Fatal("missing key invented")
	}
	if x, ok := get("empty").AsList(); !ok || len(x) != 0 {
		t.Fatal("empty list lost")
	}
	bytes[0] = 42
	source["large"] = 1
	if x, _ := get("bytes").AsBytes(); x[0] != 0 {
		t.Fatal("converted bytes alias the source")
	}
	if x, _ := get("large").AsInt(); x != 9007199254740993 {
		t.Fatal("converted object aliases the source")
	}
}

type customString string

func (customString) String() string { panic("conversion must not invoke Stringer") }

type customRecord struct{ Number int64 }

func (customRecord) MarshalJSON() ([]byte, error) { panic("conversion must not invoke MarshalJSON") }

func TestConversionDoesNotInvokeHostMethods(t *testing.T) {
	if value, _ := convert(t, customString("text"), nil).AsString(); value != "text" {
		t.Fatal(value)
	}
	fields := Fields{reflect.TypeFor[customRecord](): {"number": "Number"}}
	value, _ := convert(t, customRecord{Number: 42}, fields).Lookup("number")
	if number, ok := value.AsInt(); !ok || number != 42 {
		t.Fatal(value)
	}
}

func TestNamedPrimitivesAndFloat32(t *testing.T) {
	type integer int32
	type octet uint8
	type octets []octet
	if value, ok := convert(t, integer(42), nil).AsInt(); !ok || value != 42 {
		t.Fatal(value)
	}
	if value, ok := convert(t, float32(0.1), nil).AsFloat(); !ok || value != float64(float32(0.1)) {
		t.Fatal("float32 was reparsed or coerced:", value)
	}
	if value, ok := convert(t, octets{1, 255}, nil).AsBytes(); !ok || !reflect.DeepEqual(value, []byte{1, 255}) {
		t.Fatal(value)
	}
}

func TestCyclesAndSharedReferences(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	s := make([]any, 1)
	s[0] = s
	var p any
	p = &p
	for name, source := range map[string]any{"map": m, "slice": s, "pointer": p} {
		t.Run(name, func(t *testing.T) { requireError(t, source, nil, hff.DefaultLimits(), hff.InputError) })
	}
	shared := map[string]any{"value": 1}
	convert(t, []any{shared, shared}, nil)
	short := make([]any, 1)
	short[0] = short[:0]
	convert(t, short, nil) // Same backing address, shorter view: no ancestor cycle.
}

func TestExplicitStructMappingAndSchema(t *testing.T) {
	type record struct {
		Age     int64
		Name    *string
		Ignored string
	}
	fields := Fields{reflect.TypeFor[record](): {"age": "Age", "name": "Name"}}
	v := convert(t, record{Age: 18}, fields)
	if _, exists := v.Lookup("Ignored"); exists {
		t.Fatal("unmapped field leaked")
	}
	typ := schema.Struct(schema.Int("age"), schema.Nullable(schema.String("name")), schema.Optional(schema.Bool("flag")))
	if _, err := hff.ValidateInput(context.Background(), typ, v, hff.ValidationConfig{}); err != nil {
		t.Fatal(err)
	}
	requireError(t, record{}, nil, hff.DefaultLimits(), hff.InputError)
	bad := Fields{reflect.TypeFor[record](): {"age": "Missing"}}
	requireError(t, record{}, bad, hff.DefaultLimits(), hff.ConfigurationError)
}

func requireError(t *testing.T, source any, fields Fields, limits hff.Limits, code hff.ErrorCode) {
	t.Helper()
	v, err := Convert(context.Background(), source, fields, limits)
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != code || v.Kind() != hff.InvalidKind {
		t.Fatalf("value=%v err=%v, want %s and no partial value", v.Kind(), err, code)
	}
}

func TestInvalidInputAndLimits(t *testing.T) {
	for _, source := range []any{hff.Value{}, uint64(math.MaxUint64), math.NaN(), math.Inf(1), map[int]int{1: 2}, func() {}, make(chan int)} {
		requireError(t, source, nil, hff.DefaultLimits(), hff.InputError)
	}
	// Reject at the container before allocating one entry per source element.
	requireError(t, make([]int, 100), nil, hff.Limits{MaxInputValues: 2}, hff.LimitError)
	requireError(t, []any{[]any{1}}, nil, hff.Limits{MaxInputDepth: 1}, hff.LimitError)
	var chain any = 1
	for range 10 {
		next := chain
		chain = &next
	}
	requireError(t, chain, nil, hff.Limits{MaxInputDepth: 4}, hff.LimitError)
	requireError(t, chain, nil, hff.Limits{MaxInputValues: 4}, hff.LimitError)
	cause := errors.New("host stopped conversion")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	v, err := Convert(ctx, []int{1}, nil, hff.DefaultLimits())
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != hff.CancelledError || !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || v.Kind() != hff.InvalidKind {
		t.Fatal(v, err)
	}
}

type cancellationProbe struct {
	context.Context
	cancel context.CancelCauseFunc
	cause  error
	polls  int
}

func (c *cancellationProbe) Err() error {
	c.polls++
	if c.polls == 4 {
		c.cancel(c.cause)
	}
	return c.Context.Err()
}

func TestCancellationDuringTraversalDiscardsPartialValue(t *testing.T) {
	cause := errors.New("conversion interrupted")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	probe := &cancellationProbe{Context: ctx, cancel: cancel, cause: cause}
	v, err := Convert(probe, []int{1, 2, 3, 4}, nil, hff.DefaultLimits())
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) || v.Kind() != hff.InvalidKind {
		t.Fatal(v, err)
	}
}

func TestConcurrentConversion(t *testing.T) {
	source := map[string]any{"items": []any{int64(3), "a"}}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 20 {
				if _, err := Convert(context.Background(), source, nil, hff.DefaultLimits()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	group.Wait()
}
