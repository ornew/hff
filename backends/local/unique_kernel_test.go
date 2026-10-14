package local

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/ornew/hff/internal/core"
)

func uniqueValues() []core.Value {
	object := func(v core.Value) core.Value { return core.Object(map[string]core.Value{"n": v}) }
	return []core.Value{
		core.Null(), core.Bool(false), core.Bool(true), core.Int(0), core.Float(math.Copysign(0, -1)), core.Float(0),
		core.Int(1), core.Float(1), core.Int(math.MinInt64), core.Float(-0x1p63), core.Int(math.MaxInt64), core.Float(0x1p63),
		core.Int(9007199254740993), core.Float(9007199254740992), core.Int(9007199254740992),
		core.Float(0.1), core.Float(math.Nextafter(0.1, 1)), core.Float(math.SmallestNonzeroFloat64),
		core.String(""), core.String("あ"), core.Bytes(nil), core.Bytes([]byte{0, 1}),
		core.Duration(0), core.Duration(time.Second), core.Timestamp(time.Unix(1, 2)), core.Timestamp(time.Unix(1, 2).In(time.FixedZone("elsewhere", 3600))),
		core.List(), core.List(core.Int(1), core.Null()), core.List(core.Float(1), core.Null()), core.List(core.Null(), core.Int(1)),
		core.Object(nil), object(core.Null()), object(core.Int(1)), object(core.Float(1)),
		core.Object(map[string]core.Value{"": core.List(core.Int(1)), "a\"b": core.Null()}),
		core.Object(map[string]core.Value{"a\"b": core.Null(), "": core.List(core.Float(1))}),
	}
}

func pairwiseUnique(e *evaluation, values []core.Value) (bool, error) {
	for index, value := range values {
		for previous := 0; previous < index; previous++ {
			equal, err := e.equal(value, values[previous])
			if err != nil || equal {
				return false, err
			}
		}
	}
	return true, nil
}

func TestUniqueFingerprintEqualityClasses(t *testing.T) {
	values := uniqueValues()
	for left, a := range values {
		for right, b := range values {
			e := evaluation{ctx: context.Background()}
			equal, err := e.equal(a, b)
			if err != nil {
				t.Fatal(err)
			}
			ak, err := e.valueFingerprint(a)
			if err != nil {
				t.Fatal(err)
			}
			bk, err := e.valueFingerprint(b)
			if err != nil {
				t.Fatal(err)
			}
			if equal && ak != bk {
				t.Fatalf("equal values %d/%d had different fingerprints", left, right)
			}
			// The distinct prefix forces the second/third equality through a
			// bucket lookup instead of the initial two-item comparison.
			got, err := e.uniqueList([]core.Value{core.String("unique-test-prefix"), a, b})
			unique, valid := got.value.AsBool()
			if err != nil || !got.present || !valid || unique == equal {
				t.Fatalf("pair %d/%d unique=%v equality=%v err=%v", left, right, unique, equal, err)
			}
			if e.generated != 0 {
				t.Fatal("unique charged generated output")
			}
		}
	}
	t.Logf("verified %d equality-class pairs", len(values)*len(values))
}

func TestUniqueFingerprintMatchesPairwiseOracle(t *testing.T) {
	random := rand.New(rand.NewPCG(53, 97))
	pool := uniqueValues()
	for trial := 0; trial < 300; trial++ {
		values := make([]core.Value, random.IntN(60))
		for i := range values {
			values[i] = pool[random.IntN(len(pool))]
		}
		oracle := evaluation{ctx: context.Background()}
		want, err := pairwiseUnique(&oracle, values)
		if err != nil {
			t.Fatal(err)
		}
		e := evaluation{ctx: context.Background()}
		got, err := e.uniqueList(values)
		match, valid := got.value.AsBool()
		if err != nil || !got.present || !valid || match != want {
			t.Fatalf("trial %d unique=%v want=%v err=%v", trial, match, want, err)
		}
	}
}

func TestFingerprintCollisionRequiresEquality(t *testing.T) {
	e := evaluation{ctx: context.Background()}
	values := []core.Value{core.Int(9007199254740993), core.List(core.Null()), core.Float(9007199254740992)}
	// Deliberately supply unequal values in one candidate bucket. A hash
	// collision must not be treated as equality, including adjacent large Ints.
	for _, bucket := range [][]int{nil, {0}, {0, 1}} {
		equal, err := e.equalCandidate(core.Float(9007199254740992), values, bucket)
		if err != nil || equal {
			t.Fatal("unequal hash candidates collapsed", bucket, equal, err)
		}
	}
	equal, err := e.equalCandidate(core.Int(9007199254740992), values, []int{0, 1, 2})
	if err != nil || !equal {
		t.Fatal("later equal candidate was missed", equal, err)
	}
	e = evaluation{ctx: context.Background(), limits: core.Limits{MaxSteps: 1}}
	_, err = e.equalCandidate(core.Int(9007199254740992), values, []int{0, 1, 2})
	var detail *core.Error
	if !errors.As(err, &detail) || detail.Code != core.LimitError {
		t.Fatal("collision comparisons escaped the work budget", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e = evaluation{ctx: ctx}
	_, err = e.equalCandidate(core.Int(1), values, []int{0})
	if !errors.As(err, &detail) || detail.Code != core.CancelledError {
		t.Fatal("collision comparisons ignored cancellation", err)
	}
}

func TestUniqueFingerprintWorkAccounting(t *testing.T) {
	for _, count := range []int{0, 1, 2, 16, 128, 512} {
		values := make([]core.Value, count)
		for i := range values {
			values[i] = core.Int(int64(i))
		}
		oracle := evaluation{ctx: context.Background()}
		want, err := pairwiseUnique(&oracle, values)
		if err != nil || !want {
			t.Fatal(want, err)
		}
		e := evaluation{ctx: context.Background()}
		got, err := e.uniqueList(values)
		unique, _ := got.value.AsBool()
		if err != nil || !unique {
			t.Fatal(unique, err)
		}
		steps := count + 1
		if count < 3 {
			steps = count * (count - 1) / 2
		}
		if e.steps != steps || e.generated != 0 {
			t.Fatalf("count%d steps=%d want%d generated=%d", count, e.steps, steps, e.generated)
		}
		t.Logf("%d unique Ints: pairwise steps=%d fingerprint steps=%d", count, oracle.steps, e.steps)
	}
}
