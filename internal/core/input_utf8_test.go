package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInputUTF8ChunkBoundaries(t *testing.T) {
	for _, prefix := range []int{0, 4093, 4094, 4095, 4096, 4097, 8191} {
		for _, suffix := range []string{
			"", "a", "é", "東京", "😀", "�", "😀東京é" + strings.Repeat("a", 8192),
			"\xff", "\x80", "\xc0\x80", "\xf0\x9f\x98", "\xed\xa0\x80",
			strings.Repeat("\x80", 8192),
		} {
			text := strings.Repeat("a", prefix) + suffix
			valid, err := ValidUTF8(context.Background(), text)
			if err != nil || valid != utf8.ValidString(text) {
				t.Fatalf("prefix=%d suffix=%q: valid=%v err=%v", prefix, suffix, valid, err)
			}
		}
	}
}

type utf8CancelContext struct {
	context.Context
	checks int
	at     int
	cancel context.CancelCauseFunc
	cause  error
}

func (c *utf8CancelContext) Err() error {
	c.checks++
	at := c.at
	if at == 0 {
		at = 4
	}
	if c.checks == at {
		c.cancel(c.cause)
	}
	return c.Context.Err()
}

func TestInputUTF8CancellationDuringScan(t *testing.T) {
	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("stop validating host input")
	ctx := &utf8CancelContext{Context: base, cancel: cancel, cause: cause}
	valid, err := ValidUTF8(ctx, strings.Repeat("東京😀", 65536))
	var detail *Error
	if valid || !errors.As(err, &detail) || detail.Code != CancelledError || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("scan did not preserve cancellation and cause: valid=%v err=%v", valid, err)
	}
	// context.Cause may check Err again while retrieving the cancellation cause.
	if ctx.checks < 4 || ctx.checks > 5 {
		t.Fatalf("scan continued after cancellation: %d checks", ctx.checks)
	}
}

func TestValidationCancelsWithinLargeStringsAndKeys(t *testing.T) {
	text := strings.Repeat("東京😀", 65536)
	for _, tc := range []struct {
		name   string
		schema Type
		value  Value
	}{
		{"string", PrimitiveType(StringType), String(text)},
		{"key", NewMapType(PrimitiveType(IntType)), Object(map[string]Value{text: Int(1)})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("stop validating host input")
			at := 4
			if tc.name == "key" {
				// Exceed all non-scan context checks for a one-entry map, so
				// this cancellation requires checks within the key's UTF-8 scan.
				at = 16
			}
			ctx := &utf8CancelContext{Context: base, cancel: cancel, cause: cause, at: at}
			input, err := ValidateInput(ctx, tc.schema, tc.value, ValidationConfig{})
			var detail *Error
			if input.Compatible(tc.schema) || !errors.As(err, &detail) || detail.Code != CancelledError || !errors.Is(err, cause) {
				t.Fatalf("validation returned a completed input or lost cause: %v", err)
			}
			if ctx.checks < at || ctx.checks > at+1 {
				t.Fatalf("validation continued after cancellation: %d checks", ctx.checks)
			}
		})
	}
}

func FuzzInputUTF8(f *testing.F) {
	for _, text := range []string{"", "東京😀", "\xff", strings.Repeat("a", 4095) + "😀", strings.Repeat("\x80", 4097)} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		valid, err := ValidUTF8(context.Background(), text)
		if err != nil || valid != utf8.ValidString(text) {
			t.Fatalf("UTF-8 validity changed: valid=%v err=%v", valid, err)
		}
	})
}
