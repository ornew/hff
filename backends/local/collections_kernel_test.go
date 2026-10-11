package local

import (
	"context"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/ornew/hff/internal/core"
)

func TestStableMixedNumericSortOracle(t *testing.T) {
	random := rand.New(rand.NewPCG(17, 29))
	number := func(v core.Value) float64 {
		if i, ok := v.AsInt(); ok {
			return float64(i)
		}
		f, _ := v.AsFloat()
		return f
	}
	for trial := 0; trial < 200; trial++ {
		input := make([]core.Value, random.IntN(80))
		for i := range input {
			value := random.IntN(21) - 10
			if random.IntN(2) == 0 {
				input[i] = core.Int(int64(value))
			} else {
				input[i] = core.Float(float64(value))
			}
		}
		want := append([]core.Value(nil), input...)
		sort.SliceStable(want, func(i, j int) bool { return number(want[i]) < number(want[j]) })
		state := evaluation{ctx: context.Background()}
		result, err := state.sortList(core.List(input...))
		if err != nil || !result.present {
			t.Fatal(result, err)
		}
		got, _ := result.value.ListView()
		if len(got) != len(want) {
			t.Fatal("length changed")
		}
		for i := range want {
			if got[i].Kind() != want[i].Kind() || number(got[i]) != number(want[i]) {
				t.Fatalf("trial %d index %d: stable subtype order changed", trial, i)
			}
		}
	}
}
