package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestDefaultDepthBoundaries(t *testing.T) {
	ctx := context.Background()
	limits := hff.DefaultLimits()
	e := newEngine(t, schema.Struct(schema.Any("x"), schema.List("items", schema.IntType)))
	checkLimit := func(t *testing.T, err error, message string) {
		t.Helper()
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.LimitError || detail.Message != message {
			t.Fatalf("expected %q limit error, got %v", message, err)
		}
	}
	t.Run("syntax", func(t *testing.T) {
		if _, err := e.Compile(ctx, strings.Repeat("!", limits.MaxSyntaxDepth)+"x"); err != nil {
			t.Fatal("syntax boundary rejected:", err)
		}
		_, err := e.Compile(ctx, strings.Repeat("!", limits.MaxSyntaxDepth+1)+"x")
		checkLimit(t, err, "syntax depth limit exceeded")
	})
	t.Run("input", func(t *testing.T) {
		value := hff.Int(1)
		// The record root is depth zero; x starts at one.
		for range limits.MaxInputDepth - 1 {
			value = hff.List(value)
		}
		record := func(x hff.Value) hff.Value {
			return hff.Object(map[string]hff.Value{"x": x, "items": hff.List()})
		}
		if _, err := e.Validate(ctx, record(value)); err != nil {
			t.Fatal("input boundary rejected:", err)
		}
		_, err := e.Validate(ctx, record(hff.List(value)))
		checkLimit(t, err, "input depth limit exceeded")
	})
	t.Run("callbacks", func(t *testing.T) {
		nested := func(depth int) string {
			return strings.Repeat("items:any(x=>", depth) + "x>0" + strings.Repeat(")", depth)
		}
		// Syntax and callback ceilings are independent. Raise only syntax depth
		// to exercise the callback default instead of failing syntax first.
		deep := newEngine(t, e.Schema(), engine.WithLimits(hff.Limits{MaxSyntaxDepth: 512}))
		if _, err := deep.Compile(ctx, nested(limits.MaxCallbackDepth)); err != nil {
			t.Fatal("callback boundary rejected:", err)
		}
		_, err := deep.Compile(ctx, nested(limits.MaxCallbackDepth+1))
		checkLimit(t, err, "callback nesting depth exceeds limit")
		_, err = e.Compile(ctx, nested(limits.MaxCallbackDepth))
		checkLimit(t, err, "syntax depth limit exceeded")
	})
	t.Run("json", func(t *testing.T) {
		// Allow one extra host-input level so serialization tests reach the
		// JSON budget instead of stopping in record validation.
		jsonEngine := newEngine(t, schema.Struct(schema.Any("x")), engine.WithLimits(hff.Limits{MaxInputDepth: limits.MaxJSONDepth + 1}))
		for _, source := range []string{`x:fromjson:exists`, `x:json:exists`} {
			program, err := jsonEngine.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := local.Build(ctx, program)
			if err != nil {
				t.Fatal(err)
			}
			for _, extra := range []int{0, 1} {
				depth := limits.MaxJSONDepth + extra
				value := hff.Int(1)
				for range depth - 1 {
					value = hff.List(value)
				}
				if source == `x:fromjson:exists` {
					value = hff.String(strings.Repeat("[", depth-1) + "1" + strings.Repeat("]", depth-1))
				}
				matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"x": value}))
				if extra == 0 {
					if err != nil || !matched {
						t.Fatalf("%s JSON boundary: %v (%v)", source, matched, err)
					}
				} else {
					checkLimit(t, err, "JSON depth limit exceeded")
					if matched {
						t.Fatal("aborted JSON operation returned a match")
					}
				}
			}
		}
	})
}
