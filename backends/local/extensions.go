package local

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

// MethodFunc implements a pure, concurrent-safe extension. Ordinary data failures
// return Null(), nil. Work and produced values beyond BaseCost must be charged.
type MethodFunc func(call *Call, receiver hff.Value, args []hff.Value) (hff.Value, error)

type MethodImplementation struct {
	declaration hff.MethodDeclaration
	function    MethodFunc
}

func Implement(declaration hff.MethodDeclaration, function MethodFunc) MethodImplementation {
	return MethodImplementation{declaration, function}
}
func WithMethods(methods ...MethodImplementation) Option {
	owned := append([]MethodImplementation(nil), methods...)
	return Option{func(c *Config) error { c.Methods = append([]MethodImplementation(nil), owned...); return nil }}
}

// Call provides cooperative cancellation and budget accounting. Charging is safe
// concurrently during an invocation; the Call must not be retained for later work.
type Call struct {
	mu      sync.Mutex
	ctx     context.Context
	state   *evaluation
	failure error
}

func (c *Call) Context() context.Context {
	if c == nil {
		return nil
	}
	return c.ctx
}
func (c *Call) ChargeSteps(n int) error           { return c.charge(n, false) }
func (c *Call) ChargeGeneratedValues(n int) error { return c.charge(n, true) }
func (c *Call) charge(n int, generated bool) error {
	if c == nil {
		return configuration("nil extension Call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		return &hff.Error{Code: hff.BackendError, Message: "extension Call is no longer active"}
	}
	if c.failure != nil {
		return c.failure
	}
	if n < 0 {
		c.failure = &hff.Error{Code: hff.BackendError, Message: "extension charges cannot be negative"}
		return c.failure
	}
	if generated {
		c.failure = c.state.charge(0)
		if c.failure == nil {
			c.failure = c.state.allocate(n)
		}
	} else {
		c.failure = c.state.charge(n)
	}
	return c.failure
}
func (c *Call) finish() error { c.mu.Lock(); defer c.mu.Unlock(); c.state = nil; return c.failure }
func snapshotMethods(methods []MethodImplementation) (map[string]MethodImplementation, error) {
	out := make(map[string]MethodImplementation, len(methods))
	for _, m := range methods {
		if !m.declaration.Valid() || m.function == nil {
			return nil, configuration("invalid method implementation")
		}
		id := m.declaration.ID()
		if _, exists := out[id]; exists {
			return nil, configuration("duplicate method implementation ID " + id)
		}
		out[id] = m
	}
	return out, nil
}
func (e *evaluation) extension(n plan.Node, method plan.Method, receiver result) (result, error) {
	if !receiver.present {
		return receiver, nil
	}
	candidates := method.SignaturesView()
	var viable []core.MethodDeclaration
	for _, s := range candidates {
		d, _ := s.Declaration()
		if receiver.value.Kind() == core.NullKind && d.NullPolicy() == core.PropagateNull {
			continue
		}
		yes, err := e.matchesType(receiver.value, d.Receiver())
		if err != nil {
			return result{}, err
		}
		if yes {
			viable = append(viable, d)
		}
	}
	if len(viable) == 0 {
		return null(), nil
	}
	args := make([]core.Value, 0, n.ChildCount()-1)
	for i := 1; i < n.ChildCount(); i++ {
		arg, err := e.child(n, i)
		if err != nil || !arg.present {
			return arg, err
		}
		next := viable[:0]
		for _, d := range viable {
			if arg.value.Kind() == core.NullKind && d.NullPolicy() == core.PropagateNull {
				continue
			}
			want, _ := d.Parameter(i - 1)
			yes, err := e.matchesType(arg.value, want)
			if err != nil {
				return result{}, err
			}
			if yes {
				next = append(next, d)
			}
		}
		viable = next
		if len(viable) == 0 {
			return null(), nil
		}
		args = append(args, arg.value)
	}
	if len(viable) != 1 {
		return result{}, e.extensionFailure(n, fmt.Errorf("ambiguous runtime method signatures"))
	}
	declaration := viable[0]
	implementation := e.methods[declaration.ID()]
	if err := e.charge(declaration.BaseCost()); err != nil {
		return result{}, err
	}
	publicArgs := make([]hff.Value, len(args))
	for i, a := range args {
		publicArgs[i] = hff.Value(a)
	}
	call := &Call{ctx: e.ctx, state: e}
	value, err := invokeMethod(implementation.function, call, hff.Value(receiver.value), publicArgs)
	if budgetErr := call.finish(); budgetErr != nil {
		err = budgetErr
	}
	if err == nil {
		err = cancelled(e.ctx)
	}
	if err != nil {
		return result{}, e.extensionFailure(n, err)
	}
	// Validate physical HFF values before the declared result type. Ordinary null
	// remains an allowed data failure, independent of the declared successful type.
	if err := e.validateExtensionValue(core.Value(value), 1); err != nil {
		return result{}, e.extensionFailure(n, err)
	}
	if core.Value(value).Kind() != core.NullKind {
		yes, err := e.matchesType(core.Value(value), declaration.Result())
		if err != nil {
			return result{}, e.extensionFailure(n, err)
		}
		if !yes {
			return result{}, e.extensionFailure(n, fmt.Errorf("extension %q returned a value outside its declared result type", declaration.ID()))
		}
	}
	return ordinary(core.Value(value)), nil
}
func (e *evaluation) extensionFailure(n plan.Node, err error) error {
	var detail *core.Error
	if errors.As(err, &detail) && (detail.Code == core.LimitError || detail.Code == core.CancelledError) {
		copy := *detail
		span := n.Span()
		if copy.Span == nil {
			copy.Span = &span
		}
		return &copy
	}
	code := core.BackendError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = core.CancelledError
	}
	span := n.Span()
	return &core.Error{Code: code, Message: err.Error(), Cause: err, Span: &span}
}

func (e *evaluation) validateExtensionValue(v core.Value, depth int) error {
	if err := e.charge(1); err != nil {
		return err
	}
	if e.limits.MaxInputDepth > 0 && depth > e.limits.MaxInputDepth {
		return &core.Error{Code: core.LimitError, Message: "extension result depth limit exceeded"}
	}
	switch v.Kind() {
	case core.InvalidKind:
		return fmt.Errorf("extension returned an invalid Value")
	case core.StringKind:
		text, _ := v.AsString()
		if err := e.charge(len(text)); err != nil {
			return err
		}
		if !utf8.ValidString(text) {
			return fmt.Errorf("extension returned invalid UTF-8")
		}
	case core.FloatKind:
		x, _ := v.AsFloat()
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("extension returned a non-finite float")
		}
	case core.TimestampKind:
		x, _ := v.AsTimestamp()
		if x.Year() < 0 || x.Year() > 9999 {
			return fmt.Errorf("extension returned an out-of-range timestamp")
		}
	case core.ListKind:
		values, _ := v.ListView()
		for _, x := range values {
			if err := e.validateExtensionValue(x, depth+1); err != nil {
				return err
			}
		}
	case core.ObjectKind:
		values, _ := v.ObjectView()
		keys, err := e.objectKeys(values)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if !utf8.ValidString(key) {
				return fmt.Errorf("extension returned an invalid object key")
			}
			if err := e.validateExtensionValue(values[key], depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func invokeMethod(fn MethodFunc, call *Call, receiver hff.Value, args []hff.Value) (value hff.Value, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			if cause, ok := failure.(error); ok {
				err = fmt.Errorf("extension panicked: %w", cause)
			} else {
				err = fmt.Errorf("extension panicked: %v", failure)
			}
			value = hff.Value{}
		}
	}()
	return fn(call, receiver, args)
}
