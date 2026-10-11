package local

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff/internal/core"
)

type regexContext struct {
	context.Context
	cancel    context.CancelCauseFunc
	cause     error
	calls, at int
}

func (c *regexContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.cancel(c.cause)
	}
	return c.Context.Err()
}

func TestRegexCancellationAtLibraryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, pattern string
		at            int
		invert        bool
	}{
		{"after compile", "a", 3, false},
		{"after invalid compile", "[", 3, true},
		{"after match", "a", 4, false},
		{"after inverted match", "z", 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("stop regex evaluation")
			probe := &regexContext{Context: ctx, cancel: cancel, cause: cause, at: tc.at}
			e := evaluation{ctx: probe, limits: core.DefaultLimits()}
			got, err := e.regex("abc", tc.pattern, tc.invert)
			var detail *core.Error
			if !errors.As(err, &detail) || detail.Code != core.CancelledError || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("regex returned a result without observing cancellation: %#v, %v", got, err)
			}
			if got.present || got.value.Kind() != core.InvalidKind {
				t.Fatalf("cancelled regex produced a partial result: %#v", got)
			}
			if e.steps != len("abc")+len(tc.pattern) {
				t.Fatalf("context checkpoints changed the step charge: %d", e.steps)
			}
		})
	}
}
