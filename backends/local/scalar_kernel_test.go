package local

import (
	"math"
	"testing"

	"github.com/ornew/hff/internal/core"
)

func TestNumberConversionBoundaries(t *testing.T) {
	negativeZero := parseNumber("-0.0")
	f, ok := negativeZero.AsFloat()
	if !ok || f != 0 || !math.Signbit(f) {
		t.Fatal("lost float negative zero")
	}
	for _, s := range []string{"9223372036854775807", "-9223372036854775808"} {
		if parseNumber(s).Kind() != core.IntKind {
			t.Fatalf("%s lost int subtype", s)
		}
	}
	for _, s := range []string{"1e-9999", "-1e-9999", "5e-324", "1.7976931348623157e308"} {
		if parseNumber(s).Kind() != core.FloatKind {
			t.Fatalf("%s rejected finite result", s)
		}
	}
	for _, s := range []string{"NaN", "Infinity", "1e309", "-9223372036854775809", "1\n", "[1]", "1_000"} {
		if parseNumber(s).Kind() != core.NullKind {
			t.Fatalf("%s accepted invalid number", s)
		}
	}
}
