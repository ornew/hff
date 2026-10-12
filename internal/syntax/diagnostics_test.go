package syntax

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func TestRecoveryReportsIndependentSyntaxErrors(t *testing.T) {
	for _, source := range []string{`age> name>`, `x>"a b"= name>`, `items:any(x=>x> x<) name>`, "`a b`> age>"} {
		t.Run(source, func(t *testing.T) {
			tree, err := ParseQuery(context.Background(), source, Config{})
			var diagnostics *hff.Diagnostics
			if tree != nil || !errors.As(err, &diagnostics) {
				t.Fatalf("expected no tree and grouped diagnostics: %T, %v", tree, err)
			}
			want := 2
			if strings.HasPrefix(source, "items:") {
				want = 3
			}
			if got := diagnostics.Errors(); len(got) != want {
				t.Fatalf("got %d diagnostics, want %d: %v", len(got), want, err)
			}
			previous := -1
			for _, err := range diagnostics.Errors() {
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Code != hff.SyntaxError || detail.Span == nil || detail.Span.Start < previous {
					t.Fatalf("unordered/untyped diagnostic: %v", err)
				}
				previous = detail.Span.Start
			}
		})
	}
}

func TestSyntaxDiagnosticMessages(t *testing.T) {
	for _, test := range []struct{ source, message string }{
		{`age>`, "expected a value after the comparison operator"},
		{`age> 10`, "without intervening whitespace"},
		{`age>10<20`, "comparisons cannot be chained"},
		{`(age>10`, "expected ')'"},
		{`items[0`, "expected ']'"},
		{`labels={"a":1`, "expected '}'"},
		{`items:any(item=>item.price>100 item.active`, "expected ')'"},
		{`name="unfinished`, "closing double quote"},
		{`age+`, "expected a value"},
		{`labels.`, "expected a field name"},
		{`name:`, "expected a method name"},
		{"`unfinished", "closing backtick"},
	} {
		t.Run(test.source, func(t *testing.T) {
			tree, err := ParseQuery(context.Background(), test.source, Config{})
			if tree != nil || err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want %q", err, test.message)
			}
		})
	}
}

func TestUnicodeDiagnosticsAreAggregatedInSourceOrder(t *testing.T) {
	for _, source := range []string{`name="\ud800" other="\udfff"`, `x={"\ud800":"\udfff"}`, `"\ud800":concat("\udfff")`} {
		tree, err := ParseQuery(context.Background(), source, Config{})
		var diagnostics *hff.Diagnostics
		if tree != nil || !errors.As(err, &diagnostics) || len(diagnostics.Errors()) != 2 {
			t.Fatalf("Unicode diagnostics were not grouped: %v", err)
		}
		items := diagnostics.Errors()
		first := items[0].(*hff.Error)
		second := items[1].(*hff.Error)
		if first.Span.Start >= second.Span.Start {
			t.Fatalf("Unicode diagnostics are out of order: %v", err)
		}
	}
}
