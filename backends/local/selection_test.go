package local_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestStableDistinctAndFirstMatch(t *testing.T) {
	object := func(value hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"n": value}) }
	for _, tc := range []struct {
		source string
		value  hff.Value
	}{
		{`x:distinct=[]`, hff.List()},
		{`x:distinct=null`, hff.Null()},
		{`x!:distinct=null`, hff.Null()},
		{`x:distinct=[null,1,false,"1"]`, hff.List(hff.Null(), hff.Int(1), hff.Float(1), hff.Bool(false), hff.String("1"), hff.Null())},
		{`x:distinct[0]:[float]:exists x:distinct:size=1`, hff.List(hff.Float(1), hff.Int(1))},
		{`x:distinct[0]:json="-0.0" x:distinct:size=1`, hff.List(hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0))},
		{`x:distinct:size=2`, hff.List(hff.Int(9007199254740993), hff.Float(9007199254740992), hff.Int(9007199254740993))},
		{`x:distinct:size=1`, hff.List(hff.Int(math.MinInt64), hff.Float(-0x1p63))},
		{`x:distinct:size=2`, hff.List(hff.Int(math.MaxInt64), hff.Float(0x1p63))},
		{`x:distinct=[[1,null],{"n":1}]`, hff.List(hff.List(hff.Int(1), hff.Null()), hff.List(hff.Float(1), hff.Null()), object(hff.Int(1)), object(hff.Float(1)))},
		{`x:distinct=[{}, {"n":null}]`, hff.List(hff.Object(nil), object(hff.Null()), hff.Object(nil))},
		{`x:distinct=["あ","い","う"]`, hff.List(hff.String("あ"), hff.String("い"), hff.String("あ"), hff.String("う"))},
		{`x:distinct:size=1`, hff.List(hff.Bytes([]byte{1, 2}), hff.Bytes([]byte{1, 2}))},
		{`x:distinct:size=1`, hff.List(hff.Timestamp(time.Unix(1, 2)), hff.Timestamp(time.Unix(1, 2).In(time.FixedZone("other", 3600))))},
		{`x:distinct:size=1`, hff.List(hff.Duration(time.Second), hff.Duration(time.Second))},
		{`x:find(v=>v>2)=3`, hff.List(hff.Int(1), hff.Int(3), hff.Int(4))},
		{`x:find(v=>v>2)=null`, hff.List(hff.Int(1))},
		{`x:find(v=>true)=null`, hff.List(hff.Null(), hff.Int(1))},
		{`x:find(v=>v!)=2`, hff.List(hff.Null(), hff.Int(0), hff.Int(2))},
		{`x:find(v=>v:exists)=false`, hff.List(hff.Null(), hff.Bool(false), hff.Int(1))},
		{`x:find(v=>true)=null`, hff.List()},
		{`x:find(v=>true)=null`, hff.Null()},
		{`x:find(v=>v.n>1)={"n":2}`, hff.List(object(hff.Int(1)), object(hff.Int(2)), object(hff.Int(3)))},
	} {
		t.Run(tc.source, func(t *testing.T) {
			filter := build(t, tc.source)
			got, err := filter.Match(context.Background(), record(tc.value, hff.Null()))
			want := tc.source != `x!:distinct=null`
			if err != nil || got != want {
				t.Fatalf("got %v %v, want %v", got, err, want)
			}
		})
	}
}

func TestSelectionBudgetsAndFindShortCircuit(t *testing.T) {
	values := make([]hff.Value, 100)
	for i := range values {
		values[i] = hff.Int(int64(i + 1))
	}
	for _, source := range []string{`x:find(v=>true)=1`, `x:find(v=>true)=null`} {
		filter := build(t, source, local.WithLimits(hff.Limits{MaxSteps: 20}))
		input := hff.List(values...)
		if source == `x:find(v=>true)=null` {
			input = hff.List(append([]hff.Value{hff.Null()}, values...)...)
		}
		got, err := filter.Match(context.Background(), record(input, hff.Null()))
		if err != nil || !got {
			t.Fatalf("find did not stop at the first match: %v %v", got, err)
		}
	}
	for _, source := range []string{`x:find(v=>false)`, `x:distinct`} {
		filter := build(t, source, local.WithLimits(hff.Limits{MaxSteps: 20}))
		_, err := filter.Match(context.Background(), record(hff.List(values...), hff.Null()))
		code(t, err, hff.LimitError)
	}
	filter := build(t, `x:distinct`, local.WithLimits(hff.Limits{MaxGeneratedValues: 2}))
	_, err := filter.Match(context.Background(), record(hff.List(hff.Int(1), hff.Int(2)), hff.Null()))
	code(t, err, hff.LimitError)
	got, err := filter.Match(context.Background(), record(hff.List(hff.Int(1), hff.Int(1)), hff.Null()))
	if err != nil || !got {
		t.Fatalf("duplicate charged generated output: %v %v", got, err)
	}
}

func TestSelectionCancellation(t *testing.T) {
	e := configured(t)
	values := make([]hff.Value, 100)
	for i := range values {
		values[i] = hff.Int(int64(i))
	}
	input, err := e.Validate(context.Background(), record(hff.List(values...), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`x:distinct`, `x:find(v=>false)`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := local.Build(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		base, cancel := context.WithCancel(context.Background())
		ctx := &countingContext{Context: base, cancel: cancel}
		got, err := filter.MatchInput(ctx, input)
		cancel()
		code(t, err, hff.CancelledError)
		if got {
			t.Fatal("cancelled selection returned a partial match")
		}
	}
}
