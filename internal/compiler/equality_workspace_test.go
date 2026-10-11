package compiler

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
)

func TestEqualityWorkspaceReusePreservesWork(t *testing.T) {
	typ := core.NewStructType(core.Field{Name: "a", Type: core.PrimitiveType(core.IntType)}, core.Field{Name: "b", Type: core.PrimitiveType(core.StringType)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := compiler{ctx: ctx, config: Config{Capabilities: capabilities.Standard().Without(capabilities.ObjectEquality), Limits: core.DefaultLimits()}}
	// A rejected check also leaves visited pairs behind. The next check must
	// reject again, rather than mistaking that workspace for a successful cache.
	for i := 0; i < 2; i++ {
		err := c.equalityCapabilities(typ, typ, nil)
		var detail *core.Error
		if !errors.As(err, &detail) || detail.Code != core.CapabilityError {
			t.Fatalf("check %d lost rejection: %v", i, err)
		}
	}
	c.config.Capabilities = capabilities.Standard().Without(capabilities.ListEquality)
	before := c.steps
	if err := c.equalityCapabilities(typ, typ, nil); err != nil {
		t.Fatal(err)
	}
	work := c.steps - before
	before = c.steps
	if err := c.equalityCapabilities(typ, typ, nil); err != nil {
		t.Fatal(err)
	}
	if work == 0 || c.steps-before != work {
		t.Fatalf("reused workspace changed work: %d then %d", work, c.steps-before)
	}
	cancel()
	if err := c.equalityCapabilities(typ, typ, nil); err == nil {
		t.Fatal("reused workspace skipped cancellation")
	}
}
