package local_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestObjectLookupKeyBudget(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("界", 1024)
	for _, present := range []bool{false, true} {
		fields := map[string]hff.Value{}
		if present {
			fields[key] = hff.Bool(true)
		}
		value := record(hff.Object(fields), hff.String(key))
		e := configured(t)
		input, err := e.Validate(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`x:has(y)`, `x:get(y)`, `x[y]`, "x.`" + key + "`"} {
			t.Run(source+map[bool]string{true: "/present", false: "/missing"}[present], func(t *testing.T) {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				bounded, err := local.Build(ctx, p, local.WithLimits(hff.Limits{MaxSteps: 2048}))
				if err != nil {
					t.Fatal(err)
				}
				matched, err := bounded.MatchInput(ctx, input)
				if matched {
					t.Fatal("budget exhaustion returned a completed match")
				}
				code(t, err, hff.LimitError)
				matched, err = bounded.Match(ctx, value)
				if matched {
					t.Fatal("ordinary Match bypassed the key budget")
				}
				code(t, err, hff.LimitError)
				generous, err := local.Build(ctx, p, local.WithLimits(hff.Limits{MaxSteps: 10000}))
				if err != nil {
					t.Fatal(err)
				}
				matched, err = generous.MatchInput(ctx, input)
				if err != nil || matched != present {
					t.Fatalf("got (%v,%v), want (%v,nil)", matched, err, present)
				}
			})
		}
	}
}

func TestObjectLookupBudgetSkipsUnevaluatedKeys(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("界", 1024)
	for _, source := range []string{`x:has(y)`, `x:get(y)`, `x[y]`, `x![y]`, `x | y:get(x)`} {
		t.Run(source, func(t *testing.T) {
			x, y, want := hff.Null(), hff.String(key), false
			if source == `x | y:get(x)` {
				x, y, want = hff.Bool(true), hff.Object(nil), true
			}
			f := build(t, source, local.WithLimits(hff.Limits{MaxSteps: 100}))
			matched, err := f.Match(ctx, record(x, y))
			if err != nil || matched != want {
				t.Fatalf("got (%v,%v), want (%v,nil)", matched, err, want)
			}
		})
	}
}

func TestObjectLookupBudgetForVariableNames(t *testing.T) {
	ctx := context.Background()
	name := strings.Repeat("界", 1024)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Bool(name))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, "`"+name+"`")
	if err != nil {
		t.Fatal(err)
	}
	input, err := e.Validate(ctx, hff.Object(map[string]hff.Value{name: hff.Bool(true)}))
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{2048, 10000} {
		f, err := local.Build(ctx, p, local.WithLimits(hff.Limits{MaxSteps: budget}))
		if err != nil {
			t.Fatal(err)
		}
		matched, err := f.MatchInput(ctx, input)
		if budget == 2048 {
			if matched {
				t.Fatal("unmetered variable lookup")
			}
			code(t, err, hff.LimitError)
		} else if err != nil || !matched {
			t.Fatalf("got (%v,%v), want (true,nil)", matched, err)
		}
	}
}
