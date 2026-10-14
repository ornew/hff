package local_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestUniqueStructuralAndNumericValues(t *testing.T) {
	object := func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"n": v}) }
	for _, tc := range []struct {
		name   string
		values []hff.Value
		want   bool
	}{
		{"empty", nil, true},
		{"null-pair", []hff.Value{hff.Null(), hff.Null()}, false},
		{"third-numeric", []hff.Value{hff.String("one"), hff.Int(1), hff.Float(1)}, false},
		{"signed-zero", []hff.Value{hff.String("zero"), hff.Float(math.Copysign(0, -1)), hff.Int(0)}, false},
		{"wide-integer", []hff.Value{hff.String("wide"), hff.Int(9007199254740993), hff.Float(9007199254740992)}, true},
		{"minimum-integer", []hff.Value{hff.String("min"), hff.Int(math.MinInt64), hff.Float(-0x1p63)}, false},
		{"maximum-integer", []hff.Value{hff.String("max"), hff.Int(math.MaxInt64), hff.Float(0x1p63)}, true},
		{"nested-numbers", []hff.Value{hff.Null(), hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null())}, false},
		{"list-order", []hff.Value{hff.Null(), hff.List(hff.Int(1), hff.Null()), hff.List(hff.Null(), hff.Int(1))}, true},
		{"object-numbers", []hff.Value{hff.Null(), object(hff.Int(1)), object(hff.Float(1))}, false},
		{"missing-null", []hff.Value{hff.Null(), hff.Object(nil), object(hff.Null())}, true},
		{"bytes", []hff.Value{hff.Null(), hff.Bytes([]byte{0, 1}), hff.Bytes([]byte{0, 1})}, false},
		{"timestamp", []hff.Value{hff.Null(), hff.Timestamp(time.Unix(1, 2)), hff.Timestamp(time.Unix(1, 2).In(time.FixedZone("other", 3600)))}, false},
		{"duration", []hff.Value{hff.Null(), hff.Duration(time.Second), hff.Duration(time.Second)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := build(t, `x:unique`)
			got, err := filter.Match(context.Background(), record(hff.List(tc.values...), hff.Null()))
			if err != nil || got != tc.want {
				t.Fatal(got, err, "want", tc.want)
			}
		})
	}
}

func TestUniqueBudgetsAndShortCircuit(t *testing.T) {
	e := configured(t)
	p, err := e.Compile(context.Background(), `x:unique`)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]hff.Value, 256)
	for index := range values {
		values[index] = hff.Int(int64(index))
	}
	input, err := e.Validate(context.Background(), record(hff.List(values...), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(context.Background(), p, local.WithLimits(hff.Limits{MaxSteps: 1000, MaxGeneratedValues: 1}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := filter.MatchInput(context.Background(), input)
	if err != nil || !got {
		t.Fatal("unique exhausted a linear work budget", got, err)
	}
	late := append(append([]hff.Value(nil), values...), hff.Float(3))
	lateInput, err := e.Validate(context.Background(), record(hff.List(late...), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	got, err = filter.MatchInput(context.Background(), lateInput)
	if err != nil || got {
		t.Fatal("late mixed-number duplicate was missed", got, err)
	}
	filter, err = local.Build(context.Background(), p, local.WithLimits(hff.Limits{MaxSteps: 20}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = filter.MatchInput(context.Background(), input)
	code(t, err, hff.LimitError)
	for _, list := range []hff.Value{
		hff.List(hff.String(strings.Repeat("long", 1000))),
		hff.List(hff.Int(1), hff.Int(1), hff.String(strings.Repeat("unread", 1000))),
		hff.List(hff.Int(1), hff.Int(2), hff.Float(2), hff.String(strings.Repeat("unread", 1000))),
	} {
		input, err := e.Validate(context.Background(), record(list, hff.Null()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := filter.MatchInput(context.Background(), input)
		items, _ := list.AsList()
		if err != nil || got != (len(items) == 1) {
			t.Fatal("unique scanned unnecessary values", got, err)
		}
	}
	filter, err = local.Build(context.Background(), p, local.WithLimits(hff.Limits{MaxSteps: 20}))
	if err != nil {
		t.Fatal(err)
	}
	input, err = e.Validate(context.Background(), record(hff.List(hff.Int(0), hff.Int(1), hff.String(strings.Repeat("hash", 1000))), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = filter.MatchInput(context.Background(), input)
	code(t, err, hff.LimitError)
}

func TestUniqueCancellationDuringFingerprinting(t *testing.T) {
	e := configured(t)
	values := make([]hff.Value, 256)
	for index := range values {
		values[index] = hff.Int(int64(index))
	}
	input, err := e.Validate(context.Background(), record(hff.List(values...), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `x:unique`)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &countingContext{Context: base, cancel: cancel}
	got, err := filter.MatchInput(ctx, input)
	code(t, err, hff.CancelledError)
	if got {
		t.Fatal("cancelled unique returned a partial match")
	}
}
