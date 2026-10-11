package local_test

import (
	"context"
	"errors"
	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
	"math"
	"sync"
	"testing"
	"time"
)

func configured(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"), schema.Any("y"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func build(t *testing.T, source string, opts ...local.Option) *local.Filter {
	t.Helper()
	p, err := configured(t).Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	f, err := local.Build(context.Background(), p, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func record(x, y hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"x": x, "y": y}) }
func code(t *testing.T, err error, want hff.ErrorCode) {
	t.Helper()
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}
func TestLocalEvaluation(t *testing.T) {
	cases := []struct {
		source string
		x, y   hff.Value
		want   bool
	}{
		{``, hff.Null(), hff.Null(), true},
		{`x`, hff.Null(), hff.Null(), false}, {`x`, hff.Int(0), hff.Null(), false}, {`x`, hff.Float(math.Copysign(0, -1)), hff.Null(), false}, {`x`, hff.Bool(false), hff.Null(), false}, {`x`, hff.String(""), hff.Null(), false}, {`x`, hff.List(), hff.Null(), false}, {`x`, hff.Object(nil), hff.Null(), false}, {`x`, hff.Bytes(nil), hff.Null(), false}, {`x`, hff.Duration(0), hff.Null(), false}, {`x`, hff.Timestamp(time.Unix(0, 0)), hff.Null(), true},
		{`x`, hff.Int(-1), hff.Null(), true}, {`!x`, hff.Int(0), hff.Null(), true},
		{`x=null`, hff.Null(), hff.Null(), true}, {`x!=null`, hff.Null(), hff.Null(), false}, {`x=y`, hff.Null(), hff.Null(), false}, {`x!=y`, hff.Null(), hff.Null(), false}, {`!(x=y)`, hff.Null(), hff.Null(), true},
		{`x+1=3`, hff.Int(2), hff.Null(), true}, {`x+1.0=3`, hff.Int(2), hff.Null(), true}, {`x/2=2.5`, hff.Int(5), hff.Null(), true}, {`x%2=-1`, hff.Int(-5), hff.Null(), true}, {`x/0=null`, hff.Int(5), hff.Null(), true}, {`x+1=null`, hff.Int(math.MaxInt64), hff.Null(), true}, {`x-1=null`, hff.Int(math.MinInt64), hff.Null(), true}, {`x*-1=null`, hff.Int(math.MinInt64), hff.Null(), true}, {`x*2=null`, hff.Int(math.MaxInt64), hff.Null(), true}, {`(-x)=null`, hff.Int(math.MinInt64), hff.Null(), true}, {`x*2=null`, hff.Float(math.MaxFloat64), hff.Null(), true}, {`x+1=null`, hff.String("s"), hff.Null(), true},
		{`x=y`, hff.Int(9007199254740993), hff.Float(9007199254740992), false}, {`x>y`, hff.Int(9007199254740993), hff.Float(9007199254740992), true}, {`x<y`, hff.Int(math.MaxInt64), hff.Float(0x1p63), true}, {`x=y`, hff.Int(math.MinInt64), hff.Float(-0x1p63), true}, {`x>y`, hff.Int(0), hff.Float(-0.5), true}, {`x<y`, hff.Int(0), hff.Float(0.5), true},
		{`x="Alice" | y>10`, hff.String("Alice"), hff.Int(0), true}, {`(x="Alice" | x="Bob") y>10`, hff.String("Alice"), hff.Int(0), false},
		{`x~"Alice"`, hff.String("Hi Alice!"), hff.Null(), true}, {`x~"^Alice$"`, hff.String("Hi Alice!"), hff.Null(), false}, {`x!~"["`, hff.String("Alice"), hff.Null(), false}, {`x~"\\C"`, hff.String("Alice"), hff.Null(), false}, {`x!~"\\C"`, hff.String("Alice"), hff.Null(), false}, {`!(x!~"[")`, hff.String("Alice"), hff.Null(), true},
		{`x.a=null`, hff.Null(), hff.Null(), true}, {`x!.a=null`, hff.Null(), hff.Null(), false}, {`!(x!.a=null)`, hff.Null(), hff.Null(), true}, {`x!`, hff.Int(0), hff.Null(), false}, {`x!+1=null`, hff.Null(), hff.Null(), false},
		{`x[-1]=3`, hff.List(hff.Int(1), hff.Int(2), hff.Int(3)), hff.Null(), true}, {`x[1.0]=2`, hff.List(hff.Int(1), hff.Int(2)), hff.Null(), true}, {`x[0.5]=null`, hff.List(hff.Int(1)), hff.Null(), true}, {`x[10]=null`, hff.List(hff.Int(1)), hff.Null(), true}, {`x[-9223372036854775808]=null`, hff.List(hff.Int(1)), hff.Null(), true}, {`x["a"]=2`, hff.Object(map[string]hff.Value{"a": hff.Int(2)}), hff.Null(), true},
		{`x=[1,null,"a"]`, hff.List(hff.Float(1), hff.Null(), hff.String("a")), hff.Null(), true}, {`x={"a":null}`, hff.Object(nil), hff.Null(), false}, {`x={"a":null}`, hff.Object(map[string]hff.Value{"a": hff.Null()}), hff.Null(), true},
		{`x:[string]="a"`, hff.String("a"), hff.Null(), true}, {`x:[number]>10`, hff.String("11"), hff.Null(), false}, {`x:[int]=1`, hff.Float(1), hff.Null(), false}, {`x:[float]=1`, hff.Float(1), hff.Null(), true}, {`x:[null]=null`, hff.Null(), hff.Null(), true}, {`x:[string]=null`, hff.Null(), hff.Null(), false},
		{`x:[list[int]]=[1,2]`, hff.List(hff.Int(1), hff.Int(2)), hff.Null(), true}, {`x:[list[int]]=[1,2]`, hff.List(hff.Int(1), hff.Float(2)), hff.Null(), false}, {`x:[list[int]]=[]`, hff.List(), hff.Null(), true}, {`x:[map[int]].a=1`, hff.Object(map[string]hff.Value{"a": hff.Int(1)}), hff.Null(), true}, {`x:[map[int]].a=1`, hff.Object(map[string]hff.Value{"a": hff.Int(1), "b": hff.String("s")}), hff.Null(), false},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			f := build(t, tc.source)
			got, err := f.Match(context.Background(), record(tc.x, tc.y))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
func TestLocalContractsAndConcurrency(t *testing.T) {
	ctx := context.Background()
	e := configured(t)
	p, err := e.Compile(ctx, `x>1`)
	if err != nil {
		t.Fatal(err)
	}
	config := &local.Config{Limits: hff.Limits{MaxSteps: 1}}
	f, err := local.Build(ctx, p, local.WithConfig(config), local.WithLimits(hff.Limits{}))
	if err != nil {
		t.Fatal(err)
	}
	config.Limits.MaxSteps = 1
	input, err := e.Validate(ctx, record(hff.Int(2), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			yes, err := f.MatchInput(ctx, input)
			if err != nil || !yes {
				t.Errorf("%v %v", yes, err)
			}
		}()
	}
	wg.Wait()
	other, err := hff.ValidateInput(ctx, schema.MapOf(schema.AnyType), record(hff.Int(2), hff.Null()), hff.ValidationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.MatchInput(ctx, other)
	code(t, err, hff.InputError)
	_, err = f.MatchInput(ctx, hff.Input{})
	code(t, err, hff.InputError)
	_, err = f.Match(ctx, hff.Object(nil))
	code(t, err, hff.InputError)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.MatchInput(canceled, input)
	code(t, err, hff.CancelledError)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("lost context cause")
	}
	_, err = local.Build(ctx, hff.Program{})
	code(t, err, hff.ConfigurationError)
	method, err := e.Compile(ctx, `x:len()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = local.Build(ctx, method)
	code(t, err, hff.BackendError)
	_, err = local.Build(ctx, p, local.WithConfig(nil))
	code(t, err, hff.ConfigurationError)
}
func TestLocalBudgetsAndShortCircuit(t *testing.T) {
	ctx := context.Background()
	rec := record(hff.Null(), hff.List())
	f := build(t, `x=null | y=[1,2,3]`, local.WithLimits(hff.Limits{MaxGeneratedValues: 1}))
	yes, err := f.Match(ctx, rec)
	if err != nil || !yes {
		t.Fatalf("short circuit: %v %v", yes, err)
	}
	f = build(t, `[x!,1,2,3]=y`, local.WithLimits(hff.Limits{MaxGeneratedValues: 1}))
	yes, err = f.Match(ctx, rec)
	if err != nil || yes {
		t.Fatalf("guard propagation: %v %v", yes, err)
	}
	f = build(t, `x=[1,2]`, local.WithLimits(hff.Limits{MaxGeneratedValues: 2}))
	_, err = f.Match(ctx, record(hff.List(), hff.Null()))
	code(t, err, hff.LimitError)
	f = build(t, `x>1`, local.WithLimits(hff.Limits{MaxSteps: 1}))
	_, err = f.Match(ctx, record(hff.Int(2), hff.Null()))
	code(t, err, hff.LimitError)
	f = build(t, `x>1`, local.WithLimits(hff.Limits{MaxSteps: 1}), local.WithUnlimitedLimits())
	yes, err = f.Match(ctx, record(hff.Int(2), hff.Null()))
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	f = build(t, `x~"a"`, local.WithLimits(hff.Limits{MaxSteps: 10}))
	_, err = f.Match(ctx, record(hff.String("aaaaaaaaaaaaaaaaaa"), hff.Null()))
	code(t, err, hff.LimitError)
}

func BenchmarkMatchInput(b *testing.B) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("age"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	p, err := e.Compile(ctx, `age>10`)
	if err != nil {
		b.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		b.Fatal(err)
	}
	input, err := e.Validate(ctx, hff.Object(map[string]hff.Value{"age": hff.Int(20)}))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		yes, err := filter.MatchInput(ctx, input)
		if err != nil || !yes {
			b.Fatal(yes, err)
		}
	}
}

func TestEquivalentEngineInputAndNullableAbsence(t *testing.T) {
	ctx := context.Background()
	f := build(t, `x>1`)
	other, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("y"), schema.Any("x"))), engine.WithCapabilities(capabilities.None()))
	if err != nil {
		t.Fatal(err)
	}
	input, err := other.Validate(ctx, record(hff.Int(2), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	yes, err := f.MatchInput(ctx, input)
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Int("x")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `x=null`)
	if err != nil {
		t.Fatal(err)
	}
	nullable, err := local.Build(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	yes, err = nullable.Match(ctx, hff.Object(nil))
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
}

func FuzzLocalMatch(f *testing.F) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"), schema.Any("y"))), engine.WithCapabilities(capabilities.Standard()), engine.WithLimits(hff.Limits{MaxQueryBytes: 2048, MaxSyntaxDepth: 24, MaxSteps: 10000, MaxGeneratedValues: 1000}))
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{`x=y`, `x+1>0`, `x~y`, `x:[list[int]]=[1]`, `x!.a=null`, `x[0]=y`} {
		f.Add(s, int64(42), []byte("abc"))
	}
	f.Fuzz(func(t *testing.T, source string, number int64, data []byte) {
		check := func(err error) bool {
			if err == nil {
				return false
			}
			var detail *hff.Error
			if !errors.As(err, &detail) {
				t.Fatalf("untyped failure: %v", err)
			}
			return true
		}
		p, err := e.Compile(context.Background(), source)
		if check(err) {
			return
		}
		filter, err := local.Build(context.Background(), p)
		if check(err) {
			return
		}
		var x hff.Value
		switch len(data) % 4 {
		case 0:
			x = hff.Int(number)
		case 1:
			x = hff.String(string(data))
		case 2:
			x = hff.List(hff.Int(number), hff.Null(), hff.String(string(data)))
		case 3:
			x = hff.Object(map[string]hff.Value{"value": hff.Bytes(data)})
		}
		_, err = filter.Match(context.Background(), record(x, hff.Float(float64(number))))
		check(err)
	})
}
