package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestInputKeysMatchLexicalOrder(t *testing.T) {
	for _, count := range []int{0, 1, 3, 17, 257} {
		m := make(map[string]Value, count)
		for i := range count {
			m[fmt.Sprintf("東京😀%d", i)] = Null()
		}
		want := make([]string, 0, len(m))
		for key := range m {
			want = append(want, key)
		}
		sort.Strings(want)
		got, err := OrderedObjectKeys(context.Background(), m)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("count=%d: order changed: %v %v", count, got, err)
		}
	}
}

func TestInputKeysCancellation(t *testing.T) {
	for _, comparison := range []bool{false, true} {
		base, cancel := context.WithCancelCause(context.Background())
		cause := errors.New("stop ordering host keys")
		ctx := &utf8CancelContext{Context: base, cancel: cancel, cause: cause}
		var err error
		if comparison {
			prefix := strings.Repeat("a", 65536)
			_, err = compareInputKeys(ctx, prefix+"x", prefix+"y")
		} else {
			m := make(map[string]Value, 100)
			for i := range 100 {
				m[fmt.Sprint(i)] = Null()
			}
			// Initial check and all key collection checks finish before cancellation
			// is triggered within the merge phase.
			ctx.at = len(m) + 4
			var keys []string
			keys, err = OrderedObjectKeys(ctx, m)
			if keys != nil {
				t.Fatal("canceled sort exposed partially ordered keys")
			}
		}
		cancel(nil)
		var detail *Error
		if !errors.As(err, &detail) || detail.Code != CancelledError || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatalf("comparison=%v: cancellation or cause lost: %v", comparison, err)
		}
	}
}

func FuzzInputKeyOrder(f *testing.F) {
	for _, text := range []string{"", "z\x00a\x00東京\x00😀", "\xff\x00\x80", strings.Repeat("a", 8192) + "\x00" + strings.Repeat("a", 8192) + "b"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 65536 {
			return
		}
		m := make(map[string]Value)
		for _, key := range strings.Split(text, "\x00") {
			m[key] = Null()
		}
		want := make([]string, 0, len(m))
		for key := range m {
			want = append(want, key)
		}
		sort.Strings(want)
		got, err := OrderedObjectKeys(context.Background(), m)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("order differs from byte-lexical order: %v", err)
		}
	})
}
