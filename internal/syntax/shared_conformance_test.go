package syntax

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/conformance"
)

func TestSharedSyntaxConformance(t *testing.T) {
	for _, tc := range conformance.Cases() {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := ParseQuery(context.Background(), tc.Source, Config{})
			if tc.ParseError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != tc.ParseError {
				t.Fatalf("expected %s, got %v", tc.ParseError, err)
			}
		})
	}
}
