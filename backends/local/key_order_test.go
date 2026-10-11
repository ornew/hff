package local

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ornew/hff/internal/core"
)

func TestLocalObjectKeyOrderingAndBudget(t *testing.T) {
	for _, n := range []int{0, 1, 3, 17, 100} {
		object := make(map[string]core.Value, n)
		want := make([]string, 0, n)
		bytes := 0
		for i := range n {
			key := fmt.Sprintf("東京%03d", n-i)
			object[key] = core.Null()
			want = append(want, key)
			bytes += len(key)
		}
		sort.Strings(want)
		e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		got, err := e.objectKeys(object)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("n=%d: %v %v", n, got, err)
		}
		work := n + bytes
		if n > 1 {
			work += n * bits.Len(uint(n))
		}
		if e.steps != work {
			t.Fatalf("ordering budget changed: got=%d want=%d", e.steps, work)
		}
	}
}

func TestLocalObjectKeyOrderingCancellation(t *testing.T) {
	for _, longPrefix := range []bool{false, true} {
		t.Run(fmt.Sprint(longPrefix), func(t *testing.T) {
			object := map[string]core.Value{}
			at := 0
			if longPrefix {
				prefix := strings.Repeat("a", 65536)
				object[prefix+"a"] = core.Null()
				object[prefix+"b"] = core.Null()
				at = 12 // All reservations and key collection finish before comparison chunks.
			} else {
				for i := range 100 {
					object[fmt.Sprint(i)] = core.Null()
				}
				at = 2*len(object) + bits.Len(uint(len(object))) + 4
			}
			cause := errors.New("object key ordering stopped")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			probe := &jsonScanContext{Context: ctx, cancel: cancel, cause: cause, at: at}
			e := evaluation{ctx: probe, limits: core.DefaultLimits()}
			keys, err := e.objectKeys(object)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("cause lost: %v", err)
			}
			if keys != nil {
				t.Fatal("partially sorted output returned")
			}
		})
	}
}
