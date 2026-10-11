package local_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestScalarMethods(t *testing.T) {
	cases := []struct {
		query string
		value hff.Value
		want  bool
	}{
		{`x:type()="null"`, hff.Null(), true},
		{`x:type()="number"`, hff.Int(1), true},
		{`x:type()="bytes"`, hff.Bytes(nil), true},
		{`x!:type()="null"`, hff.Null(), false},
		{`x:exists()`, hff.Int(0), true},
		{`x:exists()`, hff.Bool(false), true},
		{`x:exists()`, hff.Null(), false},
		{`x!:[string]:exists()`, hff.Int(0), false},
		{`x:and(y!)=false`, hff.Bool(false), true},
		{`x:or(y!)=true`, hff.Bool(true), true},
		{`x:and(true)=null`, hff.Null(), true},
		{`x:not()`, hff.Bool(false), true},

		{`x:round()=3`, hff.Float(2.5), true},
		{`x:round()=-3`, hff.Float(-2.5), true},
		{`x:round():[int]=3`, hff.Int(3), true},
		{`x:round():[float]=3`, hff.Float(2.5), true},
		{`x:ceil()=-2`, hff.Float(-2.5), true},
		{`x:floor()=-3`, hff.Float(-2.5), true},
		{`x:len()=2`, hff.String("e\u0301"), true},
		{`x:len()=1`, hff.String("😀"), true},
		{`x:len()=3`, hff.Bytes([]byte{0, 1, 2}), true},
		{`x:lower()="äbc"`, hff.String("ÄBC"), true},
		{`x:upper()="ßABC"`, hff.String("ßabc"), true},
		{`x:[string]:contains("a","c")`, hff.String("abc"), true},
		{`x:[string]:contains("a","z")`, hff.String("abc"), false},
		{`x:[string]:contains("a")`, hff.List(hff.String("a")), false},
		{`x:startswith("ab") x:endswith("bc")`, hff.String("abc"), true},
		{`x:concat("def")="abcdef"`, hff.String("abc"), true},
		{`x:[string]:concat(null)=null`, hff.String("abc"), true},
		{`x:bool()`, hff.String("true"), true},
		{`x:bool()=false`, hff.String("false"), true},
		{`x:bool()=null`, hff.String("True"), true},
		{`x:bool()=null`, hff.String(" true"), true},
		{`x:number():[int]=12`, hff.String("12"), true},
		{`x:number():[float]=12`, hff.String("12.0"), true},
		{`x:number()=null`, hff.String("01"), true},
		{`x:number()=null`, hff.String(" 1"), true},
		{`x:number()=null`, hff.String("1 "), true},
		{`x:number()=null`, hff.String("+1"), true},
		{`x:number()=null`, hff.String(".5"), true},
		{`x:number()=null`, hff.String("true"), true},
		{`x:number()=null`, hff.String("9223372036854775808"), true},
		{`x:number()=null`, hff.String("1e9999"), true},
		{`x:number()=0`, hff.String("1e-9999"), true},
		{`(1/x:number())=null`, hff.String("-0.0"), true},
		{`x:number()>0`, hff.String("5e-324"), true},
		{`x:matches("Alice")`, hff.String("Hi Alice!"), true},
		{`x:matches("^Alice$")`, hff.String("Hi Alice!"), false},
		{`x:matches("[")=null`, hff.String("Alice"), true},
		{`x:matches("\\C")=null`, hff.String("Alice"), true},
		{`x:in(1,2,3)`, hff.Float(2), true},
		{`x:in(9007199254740992.0)`, hff.Int(9007199254740993), false},
		{`x:in([1,null])`, hff.List(hff.Float(1), hff.Null()), true},
		{`x:in(null)=null`, hff.Null(), true},
		{`x:in(null,1)`, hff.Int(1), true},
		{`x:round()=null`, hff.String("2"), true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			filter := build(t, tc.query)
			got, err := filter.Match(context.Background(), record(tc.value, hff.Null()))
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestScalarSupportAndBudgets(t *testing.T) {
	ctx := context.Background()
	e := configured(t)
	p, err := e.Compile(ctx, `x:contains("a")`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = local.Build(ctx, p)
	code(t, err, hff.BackendError) // Any also resolves list.contains, not implemented yet.
	for _, q := range []string{`x:type()="string"`, `x:exists()`} {
		f := build(t, q, local.WithLimits(hff.Limits{MaxSteps: 20}))
		got, err := f.Match(ctx, record(hff.String(strings.Repeat("a", 1000)), hff.Null()))
		if err != nil || !got {
			t.Fatalf("%s: %v %v", q, got, err)
		}
	}
	f := build(t, `x:lower()`, local.WithLimits(hff.Limits{MaxSteps: 20}))
	_, err = f.Match(ctx, record(hff.String(strings.Repeat("a", 1000)), hff.Null()))
	code(t, err, hff.LimitError)
	// A boolean receiver can skip a costly argument entirely.
	f = build(t, `x:and(y:lower()="a")=false`, local.WithLimits(hff.Limits{MaxSteps: 20}))
	got, err := f.Match(ctx, record(hff.Bool(false), hff.String(strings.Repeat("a", 1000))))
	if err != nil || !got {
		t.Fatalf("short circuit: %v %v", got, err)
	}
	// Dynamic argument incompatibility establishes null before the next argument.
	f = build(t, `x:[string]:contains(y,x:lower())=null`, local.WithLimits(hff.Limits{MaxSteps: 20}))
	got, err = f.Match(ctx, record(hff.String(strings.Repeat("a", 1000)), hff.Int(1)))
	if err != nil || !got {
		t.Fatalf("argument propagation: %v %v", got, err)
	}
	f = build(t, `x:and(y)=null`)
	got, err = f.Match(ctx, record(hff.Bool(true), hff.Int(1)))
	if err != nil || !got {
		t.Fatalf("strict boolean argument: %v %v", got, err)
	}
	f = build(t, `x:round():[float]=0`)
	got, err = f.Match(ctx, record(hff.Float(math.Copysign(0, -1)), hff.Null()))
	if err != nil || !got {
		t.Fatal(got, err)
	}
}
