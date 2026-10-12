package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/engine"
)

func TestCompileAggregatesIndependentFailures(t *testing.T) {
	e := newEngine(t, compilerSchema())
	for _, test := range []struct {
		source string
		count  int
	}{
		{`missing=1 other=2`, 2},
		{`missing=1 missing=2`, 2},
		{`missing+other>1`, 2},
		{`items=[missing,other]`, 2},
		{`x={"a":missing,"b":other}`, 2},
		{`x={"a":1,"a":2,"b":other}`, 2},
		{`name:contains(missing,other)`, 2},
		{`items:any(x=>x:no_such_method | x:other_unknown_method)`, 2},
		{`1=2 missing=1`, 2},
		{`age> name>`, 2},
	} {
		t.Run(test.source, func(t *testing.T) {
			program, err := e.Compile(context.Background(), test.source)
			var diagnostics *hff.Diagnostics
			if program.Valid() || !errors.As(err, &diagnostics) {
				t.Fatalf("got program=%t, err=%v; expected grouped failure", program.Valid(), err)
			}
			items := diagnostics.Errors()
			if len(items) != test.count {
				t.Fatalf("got %d diagnostics, want %d: %v", len(items), test.count, err)
			}
			last := -1
			for _, item := range items {
				var detail *hff.Error
				if !errors.As(item, &detail) || detail.Span == nil || detail.Span.Start < last {
					t.Fatalf("invalid diagnostic order: %v", err)
				}
				last = detail.Span.Start
			}
			items[0] = nil
			if diagnostics.Errors()[0] == nil {
				t.Fatal("diagnostics exposed their slice")
			}
			var first *hff.Error
			if !errors.As(err, &first) {
				t.Fatal("existing errors.As contract broken")
			}
		})
	}
}

func TestCompileDiagnosticsDoNotHideBudgetAbort(t *testing.T) {
	e := newEngine(t, compilerSchema(), engine.WithLimits(hff.Limits{MaxCompileSteps: 8}))
	p, err := e.Compile(context.Background(), `missing=1 other=2`)
	var detail *hff.Error
	var diagnostics *hff.Diagnostics
	if p.Valid() || !errors.As(err, &detail) || detail.Code != hff.LimitError || errors.As(err, &diagnostics) {
		t.Fatalf("budget abort was confused with diagnostics: %v", err)
	}
}
