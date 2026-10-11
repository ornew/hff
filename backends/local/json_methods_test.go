package local_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestJSONMethods(t *testing.T) {
	cases := []struct {
		query string
		value hff.Value
		want  bool
	}{
		{`x:tojson="null"`, hff.Null(), true},
		{`x:tostring="null"`, hff.Null(), true},
		{`x!:tojson:exists`, hff.Null(), false},
		{`x:[int]:tostring:exists`, hff.String("a"), false},
		{`x:tostring="hello"`, hff.String("hello"), true},
		{`x:tojson="\"hello\""`, hff.String("hello"), true},
		{`x:tojson="1.0"`, hff.Float(1), true},
		{`x:tojson="-0.0"`, hff.Float(math.Copysign(0, -1)), true},
		{`x:tojson:fromjson:[float]=1`, hff.Float(1), true},
		{`x:fromjson:[int]=1`, hff.String("1"), true},
		{`x:fromjson:[float]=1`, hff.String("1.0"), true},
		{`x:tojson="\"AAE=\""`, hff.Bytes([]byte{0, 1}), true},
		{`x:tostring="AAE="`, hff.Bytes([]byte{0, 1}), true},
		{`x:tojson="{\"a\":null,\"b\":1}"`, hff.Object(map[string]hff.Value{"b": hff.Int(1), "a": hff.Null()}), true},
		{`x:tostring="[1,\"a\",true]"`, hff.List(hff.Int(1), hff.String("a"), hff.Bool(true)), true},
		{`x:fromjson=null`, hff.Int(1), true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got, err := build(t, tc.query).Match(context.Background(), record(tc.value, hff.Null()))
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	for _, bad := range []string{`{"a":1,"\u0061":2}`, `"\ud800"`, `[1,]`, `01`, `1e999`, `9223372036854775808`} {
		got, err := build(t, `x:fromjson=null`).Match(context.Background(), record(hff.String(bad), hff.Null()))
		if err != nil || !got {
			t.Fatalf("invalid JSON %q: %v %v", bad, got, err)
		}
	}
	_, err := build(t, `x:fromjson=null`).Match(context.Background(), record(hff.String(string([]byte{34, 255, 34})), hff.Null()))
	code(t, err, hff.InputError)
}

func TestJSONPublicBudgets(t *testing.T) {
	ctx := context.Background()
	if hff.DefaultLimits().MaxJSONDepth != 64 {
		t.Fatal("JSON default depth must be 64")
	}
	for _, depth := range []int{64, 65} {
		text := strings.Repeat("[", depth-1) + "1" + strings.Repeat("]", depth-1)
		got, err := build(t, `x:fromjson:exists`).Match(ctx, record(hff.String(text), hff.Null()))
		if depth == 65 {
			code(t, err, hff.LimitError)
		} else if err != nil || !got {
			t.Fatalf("default depth boundary: %v %v", got, err)
		}
	}
	for _, q := range []string{`x:fromjson:exists`, `x:tojson:exists`, `x:tostring:exists`} {
		var v hff.Value
		if strings.Contains(q, "fromjson") {
			v = hff.String(`[[1]]`)
		} else {
			v = hff.List(hff.List(hff.Int(1)))
		}
		f := build(t, q, local.WithLimits(hff.Limits{MaxJSONDepth: 2}))
		_, err := f.Match(ctx, record(v, hff.Null()))
		code(t, err, hff.LimitError)
		f = build(t, q, local.WithLimits(hff.Limits{MaxJSONDepth: 3}))
		got, err := f.Match(ctx, record(v, hff.Null()))
		if err != nil || !got {
			t.Fatalf("depth boundary %s: %v %v", q, got, err)
		}
		f = build(t, q, local.WithLimits(hff.Limits{MaxJSONDepth: 1}), local.WithUnlimitedLimits())
		got, err = f.Match(ctx, record(v, hff.Null()))
		if err != nil || !got {
			t.Fatalf("unlimited %s: %v %v", q, got, err)
		}
	}
	// Parsing a string's JSON structure is independent of host input depth.
	f := build(t, `x:fromjson:exists`, local.WithLimits(hff.Limits{MaxInputDepth: 2, MaxJSONDepth: 4}))
	got, err := f.Match(ctx, record(hff.String(`[[[1]]]`), hff.Null()))
	if err != nil || !got {
		t.Fatalf("independent depth: %v %v", got, err)
	}
	for _, budget := range []int{2, 3} {
		f = build(t, `x:fromjson:exists`, local.WithLimits(hff.Limits{MaxGeneratedValues: budget}))
		got, err = f.Match(ctx, record(hff.String(`[1,2]`), hff.Null()))
		if budget == 2 {
			code(t, err, hff.LimitError)
		} else if err != nil || !got {
			t.Fatalf("generated boundary: %v %v", got, err)
		}
	}
	f = build(t, `x:fromjson`, local.WithLimits(hff.Limits{MaxSteps: 20}))
	_, err = f.Match(ctx, record(hff.String(strings.Repeat(" ", 100)+`1`), hff.Null()))
	code(t, err, hff.LimitError)
	_, err = local.Build(ctx, mustJSONProgram(t), local.WithLimits(hff.Limits{MaxJSONDepth: -1}))
	code(t, err, hff.ConfigurationError)
}

func mustJSONProgram(t *testing.T) hff.Program {
	t.Helper()
	p, err := configured(t).Compile(context.Background(), `x:fromjson`)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
