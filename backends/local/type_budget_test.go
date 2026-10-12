package local

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ornew/hff/internal/core"
)

func optionalWideType(n int) core.Type {
	fields := make([]core.Field, n)
	for i := range fields {
		fields[i] = core.Field{Name: fmt.Sprintf("f%03d", i), Type: core.PrimitiveType(core.IntType), Optional: true}
	}
	return core.NewStructType(fields...)
}

func TestStructuralTypeChecksBudgetMissingFields(t *testing.T) {
	typ := optionalWideType(100)
	for _, limit := range []int{100, 101} {
		e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		e.limits.MaxSteps = limit
		yes, err := e.matchesType(core.Object(nil), typ)
		if limit == 100 {
			var detail *core.Error
			if yes || !errors.As(err, &detail) || detail.Code != core.LimitError {
				t.Fatalf("unmetered fields: yes=%v error=%v", yes, err)
			}
		} else if !yes || err != nil || e.steps != 101 {
			t.Fatalf("field work boundary: yes=%v steps=%d error=%v", yes, e.steps, err)
		}
	}
}

func TestStructuralTypeChecksCancelWhileSkippingMissingFields(t *testing.T) {
	cause := errors.New("structural type check stopped")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	probe := &jsonScanContext{Context: ctx, cancel: cancel, cause: cause}
	e := evaluation{ctx: probe, limits: core.DefaultLimits()}
	yes, err := e.matchesType(core.Object(nil), optionalWideType(100))
	if yes || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("missing field walk ignored cancellation: yes=%v error=%v", yes, err)
	}
}
