package local_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func extension(t *testing.T, receiver, result hff.Type, policy hff.NullPolicy, cost int, params ...hff.Type) hff.MethodDeclaration {
	t.Helper()
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "service.custom.v1", Name: "custom", Receiver: receiver, Parameters: params, Result: result, NullPolicy: policy, BaseCost: cost})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func extensionProgram(t *testing.T, q string, ds ...hff.MethodDeclaration) hff.Program {
	t.Helper()
	policy := capabilities.Standard()
	for _, d := range ds {
		policy = policy.Allow(capabilities.Method(d.ID()))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"), schema.Any("y"))), engine.WithCapabilities(policy), engine.WithMethods(ds...))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestLocalExtensionNullTypesAndGuards(t *testing.T) {
	d := extension(t, schema.StringType, schema.StringType, hff.PropagateNull, 1, schema.StringType)
	for _, tc := range []struct {
		q     string
		x, y  hff.Value
		want  bool
		calls int
	}{
		{`x:custom(y)="ab"`, hff.String("a"), hff.String("b"), true, 1},
		{`x:custom(y)=null`, hff.Null(), hff.String("b"), true, 0},
		{`x:custom(y)=null`, hff.Int(1), hff.String("b"), true, 0},
		{`x:custom(y)=null`, hff.String("a"), hff.Null(), true, 0},
		{`x:custom(y)=null`, hff.String("a"), hff.Int(1), true, 0},
		{`x!:custom(y)=null`, hff.Null(), hff.String("b"), false, 0},
		{`x:custom(y!)=null`, hff.String("a"), hff.Null(), false, 0},
	} {
		t.Run(tc.q+tc.x.Kind().String()+tc.y.Kind().String(), func(t *testing.T) {
			calls := 0
			f, err := local.Build(context.Background(), extensionProgram(t, tc.q, d), local.WithMethods(local.Implement(d, func(c *local.Call, r hff.Value, args []hff.Value) (hff.Value, error) {
				calls++
				a, _ := r.AsString()
				b, _ := args[0].AsString()
				return hff.String(a + b), nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.Match(context.Background(), record(tc.x, tc.y))
			if err != nil || got != tc.want || calls != tc.calls {
				t.Fatalf("got %v %v calls=%d", got, err, calls)
			}
		})
	}
	nullable := schema.Union(schema.StringType, schema.NullType)
	d = extension(t, nullable, schema.StringType, hff.InvokeNull, 1, nullable)
	calls := 0
	f, err := local.Build(context.Background(), extensionProgram(t, `x:custom(y)="observed"`, d), local.WithMethods(local.Implement(d, func(c *local.Call, r hff.Value, args []hff.Value) (hff.Value, error) {
		calls++
		if r.Kind() != hff.NullKind || args[0].Kind() != hff.NullKind {
			t.Fatal("null not passed")
		}
		return hff.String("observed"), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Match(context.Background(), record(hff.Null(), hff.Null()))
	if err != nil || !got || calls != 1 {
		t.Fatal(got, err, calls)
	}
}
func TestLocalExtensionBuildContracts(t *testing.T) {
	d := extension(t, schema.StringType, schema.StringType, hff.PropagateNull, 1)
	p := extensionProgram(t, `x:custom`, d)
	_, err := local.Build(context.Background(), p)
	code(t, err, hff.BackendError)
	different := extension(t, schema.StringType, schema.BoolType, hff.PropagateNull, 1)
	fn := func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) { return hff.String("a"), nil }
	_, err = local.Build(context.Background(), p, local.WithMethods(local.Implement(different, fn)))
	code(t, err, hff.BackendError)
	for _, methods := range [][]local.MethodImplementation{{local.Implement(d, nil)}, {local.Implement(hff.MethodDeclaration{}, fn)}, {local.Implement(d, fn), local.Implement(d, fn)}} {
		_, err = local.Build(context.Background(), p, local.WithMethods(methods...))
		code(t, err, hff.ConfigurationError)
	}
	methods := []local.MethodImplementation{local.Implement(d, fn)}
	option := local.WithMethods(methods...)
	methods[0] = local.MethodImplementation{}
	f, err := local.Build(context.Background(), p, option)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Match(context.Background(), record(hff.String("a"), hff.Null()))
	if err != nil || !got {
		t.Fatal(got, err)
	}
}
func TestLocalExtensionFailureContracts(t *testing.T) {
	d := extension(t, schema.AnyType, schema.StringType, hff.InvokeNull, 1)
	p := extensionProgram(t, `x:custom`, d)
	sentinel := errors.New("implementation failed")
	for _, value := range []hff.Value{{}, hff.Int(1), hff.Float(math.Inf(1)), hff.String(string([]byte{255}))} {
		f, err := local.Build(context.Background(), p, local.WithMethods(local.Implement(d, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) { return value, nil })))
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
		code(t, err, hff.BackendError)
	}
	f, err := local.Build(context.Background(), p, local.WithMethods(local.Implement(d, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) { return hff.Null(), sentinel })))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
	code(t, err, hff.BackendError)
	if !errors.Is(err, sentinel) {
		t.Fatal("error cause lost")
	}
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Span == nil {
		t.Fatal("source span missing")
	}
	f, err = local.Build(context.Background(), extensionProgram(t, `x:custom=null`, d), local.WithMethods(local.Implement(d, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) { return hff.Null(), nil })))
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Match(context.Background(), record(hff.Null(), hff.Null()))
	if err != nil || !got {
		t.Fatal("ordinary null failure", got, err)
	}
}
func TestLocalExtensionBudgetsAndCancellation(t *testing.T) {
	d := extension(t, schema.AnyType, schema.StringType, hff.InvokeNull, 1)
	p := extensionProgram(t, `x:custom`, d)
	for _, fn := range []local.MethodFunc{
		func(c *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) {
			_ = c.ChargeSteps(1000)
			return hff.String("yes"), nil
		},
		func(c *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) {
			_ = c.ChargeGeneratedValues(1000)
			return hff.String("yes"), nil
		},
	} {
		f, err := local.Build(context.Background(), p, local.WithMethods(local.Implement(d, fn)), local.WithLimits(hff.Limits{MaxSteps: 100, MaxGeneratedValues: 100}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
		code(t, err, hff.LimitError)
	}
	cost := extension(t, schema.AnyType, schema.StringType, hff.InvokeNull, 1000)
	calls := 0
	f, err := local.Build(context.Background(), extensionProgram(t, `x:custom`, cost), local.WithLimits(hff.Limits{MaxSteps: 100}), local.WithMethods(local.Implement(cost, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) { calls++; return hff.String("yes"), nil })))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
	code(t, err, hff.LimitError)
	if calls != 0 {
		t.Fatal("called beyond base budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	f, err = local.Build(context.Background(), p, local.WithMethods(local.Implement(d, func(c *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) {
		if c.Context() != ctx {
			t.Fatal("context lost")
		}
		cancel()
		return hff.String("yes"), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(ctx, record(hff.Null(), hff.Null()))
	code(t, err, hff.CancelledError)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation cause lost")
	}
}
func TestLocalExtensionPreservesHostCancellationCause(t *testing.T) {
	d := extension(t, schema.AnyType, schema.StringType, hff.InvokeNull, 1)
	p := extensionProgram(t, `x:custom`, d)
	for _, result := range []string{"value", "status", "cause"} {
		t.Run(result, func(t *testing.T) {
			cause := errors.New("host stopped the extension")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			f, err := local.Build(context.Background(), p, local.WithMethods(local.Implement(d, func(c *local.Call, _ hff.Value, _ []hff.Value) (hff.Value, error) {
				cancel(cause)
				switch result {
				case "status":
					return hff.Value{}, c.Context().Err()
				case "cause":
					return hff.Value{}, context.Cause(c.Context())
				}
				return hff.String("yes"), nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			matched, err := f.Match(ctx, record(hff.Null(), hff.Null()))
			code(t, err, hff.CancelledError)
			if matched || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("lost extension cancellation cause or returned a match: %v", err)
			}
		})
	}
}

func TestLocalExtensionConcurrentCallsAndLifetime(t *testing.T) {
	d := extension(t, schema.StringType, schema.StringType, hff.PropagateNull, 1)
	var calls atomic.Int64
	var saved *local.Call
	f, err := local.Build(context.Background(), extensionProgram(t, `x:custom="ABC"`, d), local.WithMethods(local.Implement(d, func(c *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) {
		calls.Add(1)
		if err := c.ChargeSteps(3); err != nil {
			return hff.Null(), err
		}
		s, _ := r.AsString()
		return hff.String(strings.ToUpper(s)), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				got, err := f.Match(context.Background(), record(hff.String("abc"), hff.Null()))
				if err != nil || !got {
					t.Error(got, err)
				}
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 160 {
		t.Fatal(calls.Load())
	}
	f, err = local.Build(context.Background(), extensionProgram(t, `x:custom`, d), local.WithMethods(local.Implement(d, func(c *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) {
		saved = c
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := c.ChargeSteps(1); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		return hff.String("yes"), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(context.Background(), record(hff.String("abc"), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	code(t, saved.ChargeSteps(1), hff.BackendError)
}

func TestLocalExtensionOverloadSelectionAndArguments(t *testing.T) {
	base := hff.MethodConfig{Name: "custom", Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1}
	base.ID = "service.text.v1"
	base.Parameters = []hff.Type{schema.StringType}
	text, err := hff.NewMethodDeclaration(base)
	if err != nil {
		t.Fatal(err)
	}
	base.ID = "service.int.v1"
	base.Parameters = []hff.Type{schema.IntType}
	integer, err := hff.NewMethodDeclaration(base)
	if err != nil {
		t.Fatal(err)
	}
	p := extensionProgram(t, `x:custom(y)=null | x:custom(y)="string" | x:custom(y)="int"`, text, integer)
	var textCalls, intCalls int
	f, err := local.Build(context.Background(), p, local.WithMethods(
		local.Implement(text, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) {
			textCalls++
			return hff.String("string"), nil
		}),
		local.Implement(integer, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) {
			intCalls++
			return hff.String("int"), nil
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []hff.Value{hff.String("s"), hff.Int(1), hff.Float(1), hff.Null()} {
		got, err := f.Match(context.Background(), record(hff.String("receiver"), value))
		if err != nil || !got {
			t.Fatal(got, err)
		}
	}
	if textCalls != 2 || intCalls != 3 {
		t.Fatal("wrong overload selection", textCalls, intCalls)
	}
	base.ID = "service.var.v1"
	base.Parameters = []hff.Type{schema.StringType}
	base.Optional = []hff.Type{schema.IntType}
	base.Variadic = schema.BoolType
	variable, err := hff.NewMethodDeclaration(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`x:custom("a")="ok"`, `x:custom("a",1)="ok"`, `x:custom("a",1,true,false)="ok"`} {
		f, err := local.Build(context.Background(), extensionProgram(t, q, variable), local.WithMethods(local.Implement(variable, func(c *local.Call, r hff.Value, args []hff.Value) (hff.Value, error) {
			if len(args) < 1 || len(args) > 4 {
				t.Fatal("argument count")
			}
			return hff.String("ok"), nil
		})))
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.Match(context.Background(), record(hff.String("x"), hff.Null()))
		if err != nil || !got {
			t.Fatal(q, got, err)
		}
	}
}
func TestLocalExtensionResultStructureAndBudget(t *testing.T) {
	object := schema.Struct(schema.Int("count"), schema.Optional(schema.String("name")))
	d := extension(t, schema.AnyType, object, hff.InvokeNull, 1)
	for _, tc := range []struct {
		value hff.Value
		want  hff.ErrorCode
	}{
		{hff.Object(map[string]hff.Value{"count": hff.Int(1)}), ""},
		{hff.Object(map[string]hff.Value{"count": hff.String("1")}), hff.BackendError},
		{hff.Object(map[string]hff.Value{"count": hff.Int(1), "extra": hff.Int(1)}), hff.BackendError},
		{hff.Object(map[string]hff.Value{"name": hff.String("a")}), hff.BackendError},
	} {
		f, err := local.Build(context.Background(), extensionProgram(t, `x:custom:exists`, d), local.WithMethods(local.Implement(d, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) { return tc.value, nil })))
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.Match(context.Background(), record(hff.Null(), hff.Null()))
		if tc.want != "" {
			code(t, err, tc.want)
		} else if err != nil || !got {
			t.Fatal(got, err)
		}
	}
	d = extension(t, schema.AnyType, schema.StringType, hff.InvokeNull, 1)
	f, err := local.Build(context.Background(), extensionProgram(t, `x:custom`, d), local.WithLimits(hff.Limits{MaxSteps: 100}), local.WithMethods(local.Implement(d, func(*local.Call, hff.Value, []hff.Value) (hff.Value, error) {
		return hff.String(strings.Repeat("a", 1000)), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
	code(t, err, hff.LimitError)
	f, err = local.Build(context.Background(), extensionProgram(t, `x:custom`, d), local.WithMethods(local.Implement(d, func(c *local.Call, r hff.Value, args []hff.Value) (hff.Value, error) {
		_ = c.ChargeSteps(-1)
		return hff.String("yes"), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
	code(t, err, hff.BackendError)
}

func TestLocalExtensionPanicIsContractFailure(t *testing.T) {
	d := extension(t, schema.AnyType, schema.AnyType, hff.InvokeNull, 1)
	var saved *local.Call
	failure := errors.New("callback panic")
	f, err := local.Build(context.Background(), extensionProgram(t, `x:custom`, d), local.WithMethods(local.Implement(d, func(call *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) { saved = call; panic(failure) })))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Match(context.Background(), record(hff.Null(), hff.Null()))
	code(t, err, hff.BackendError)
	if !errors.Is(err, failure) {
		t.Fatal("panic cause lost")
	}
	code(t, saved.ChargeSteps(1), hff.BackendError)
}
