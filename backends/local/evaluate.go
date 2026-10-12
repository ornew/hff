package local

import (
	"context"
	"math"

	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

// present=false is guard failure, distinct from an ordinary null value.
type result struct {
	value   core.Value
	present bool
}

func ordinary(v core.Value) result { return result{v, true} }
func null() result                 { return ordinary(core.Null()) }

type evaluation struct {
	ctx              context.Context
	record           core.Value
	locals           map[plan.BindingID]core.Value
	limits           core.Limits
	timeZones        zoneSnapshot
	methods          map[string]MethodImplementation
	steps, generated int
}

func (e *evaluation) charge(n int) error {
	if err := cancelled(e.ctx); err != nil {
		return err
	}
	if n < 0 || e.steps > math.MaxInt-n {
		return &core.Error{Code: core.LimitError, Message: "evaluation step count overflow"}
	}
	e.steps += n
	if e.limits.MaxSteps > 0 && e.steps > e.limits.MaxSteps {
		return &core.Error{Code: core.LimitError, Message: "evaluation step limit exceeded"}
	}
	return nil
}
func (e *evaluation) allocate(n int) error {
	if n < 0 || e.generated > math.MaxInt-n {
		return &core.Error{Code: core.LimitError, Message: "generated value count overflow"}
	}
	e.generated += n
	if e.limits.MaxGeneratedValues > 0 && e.generated > e.limits.MaxGeneratedValues {
		return &core.Error{Code: core.LimitError, Message: "generated value limit exceeded"}
	}
	return nil
}
func (e *evaluation) child(n plan.Node, index int) (result, error) {
	child, _ := n.Child(index)
	return e.eval(child)
}
func (e *evaluation) eval(n plan.Node) (result, error) {
	if err := e.charge(1); err != nil {
		return result{}, err
	}
	switch n.Kind() {
	case plan.MatchAllNode:
		return ordinary(core.Bool(true)), nil
	case plan.LiteralNode:
		v, _ := n.Literal()
		return ordinary(v), nil
	case plan.VariableNode:
		b, _ := n.Binding()
		if err := e.charge(len(b.Name())); err != nil {
			return result{}, err
		}
		v, exists := e.record.Lookup(b.Name())
		if !exists {
			v = core.Null()
		}
		return ordinary(v), nil
	case plan.ParameterNode:
		binding, _ := n.Binding()
		value, exists := e.locals[binding.ID()]
		if !exists {
			return result{}, &core.Error{Code: core.BackendError, Message: "unbound callback parameter"}
		}
		return ordinary(value), nil
	case plan.FieldNode:
		receiver, err := e.child(n, 0)
		if err != nil || !receiver.present {
			return receiver, err
		}
		name, _ := n.Field()
		if receiver.value.Kind() == core.ObjectKind {
			if err := e.charge(len(name)); err != nil {
				return result{}, err
			}
		}
		v, exists := receiver.value.Lookup(name)
		if !exists {
			v = core.Null()
		}
		return ordinary(v), nil
	case plan.IndexNode:
		receiver, err := e.child(n, 0)
		if err != nil || !receiver.present || receiver.value.Kind() == core.NullKind {
			return receiver, err
		}
		index, err := e.child(n, 1)
		if err != nil || !index.present {
			return index, err
		}
		if receiver.value.Kind() == core.ObjectKind {
			if key, ok := index.value.AsString(); ok {
				if err := e.charge(len(key)); err != nil {
					return result{}, err
				}
			}
		}
		return ordinary(indexValue(receiver.value, index.value)), nil
	case plan.GuardNode:
		v, err := e.child(n, 0)
		if err != nil || !v.present {
			return v, err
		}
		if v.value.Kind() == core.NullKind {
			return result{}, nil
		}
		return v, nil
	case plan.TypeGuardNode:
		v, err := e.child(n, 0)
		if err != nil || !v.present {
			return v, err
		}
		constraint, _ := n.Constraint()
		yes, err := e.matchesType(v.value, constraint)
		if err != nil {
			return result{}, err
		}
		if !yes {
			return result{}, nil
		}
		return v, nil
	case plan.BoolNode, plan.NotNode:
		v, err := e.child(n, 0)
		if err != nil {
			return result{}, err
		}
		yes := truth(v)
		if n.Kind() == plan.NotNode {
			yes = !yes
		}
		return ordinary(core.Bool(yes)), nil
	case plan.AndNode, plan.OrNode:
		and := n.Kind() == plan.AndNode
		for i := 0; i < n.ChildCount(); i++ {
			v, err := e.child(n, i)
			if err != nil {
				return result{}, err
			}
			yes := truth(v)
			if (and && !yes) || (!and && yes) {
				return ordinary(core.Bool(yes)), nil
			}
		}
		return ordinary(core.Bool(and)), nil
	case plan.NullTestNode:
		v, err := e.child(n, 0)
		if err != nil {
			return result{}, err
		}
		op, _ := n.Operator()
		yes := false
		if v.present {
			yes = v.value.Kind() == core.NullKind
			if op == plan.NotEqual {
				yes = !yes
			}
		}
		return ordinary(core.Bool(yes)), nil
	case plan.NegateNode:
		v, err := e.child(n, 0)
		if err != nil || !v.present || v.value.Kind() == core.NullKind {
			return v, err
		}
		if x, ok := v.value.AsInt(); ok {
			if x == math.MinInt64 {
				return null(), nil
			}
			return ordinary(core.Int(-x)), nil
		}
		if x, ok := v.value.AsFloat(); ok {
			return ordinary(core.Float(-x)), nil
		}
		return null(), nil
	case plan.BinaryNode:
		return e.binary(n)
	case plan.CallNode:
		return e.call(n)
	case plan.ListNode, plan.ObjectNode:
		if err := e.allocate(1); err != nil {
			return result{}, err
		}
		var values []core.Value
		for i := 0; i < n.ChildCount(); i++ {
			v, err := e.child(n, i)
			if err != nil || !v.present {
				return v, err
			}
			if err := e.allocate(1); err != nil {
				return result{}, err
			}
			values = append(values, v.value)
		}
		if n.Kind() == plan.ListNode {
			return ordinary(core.OwnedList(values)), nil
		}
		keys, _ := n.Keys()
		obj := make(map[string]core.Value, len(keys))
		for i, k := range keys {
			obj[k] = values[i]
		}
		return ordinary(core.OwnedObject(obj)), nil
	}
	return result{}, &core.Error{Code: core.BackendError, Message: "unsupported local plan node"}
}
func truth(v result) bool {
	if !v.present {
		return false
	}
	switch v.value.Kind() {
	case core.BoolKind:
		b, _ := v.value.AsBool()
		return b
	case core.IntKind:
		x, _ := v.value.AsInt()
		return x != 0
	case core.FloatKind:
		x, _ := v.value.AsFloat()
		return x != 0
	case core.StringKind:
		s, _ := v.value.AsString()
		return s != ""
	case core.BytesKind:
		b, _ := v.value.BytesView()
		return len(b) > 0
	case core.ListKind:
		xs, _ := v.value.ListView()
		return len(xs) > 0
	case core.ObjectKind:
		xs, _ := v.value.ObjectView()
		return len(xs) > 0
	case core.DurationKind:
		x, _ := v.value.AsDuration()
		return x != 0
	case core.TimestampKind:
		return true
	default:
		return false
	}
}
func indexValue(receiver, index core.Value) core.Value {
	if xs, ok := receiver.ListView(); ok {
		offset, valid := integerIndex(index)
		if !valid {
			return core.Null()
		}
		if offset < 0 {
			offset += int64(len(xs))
		}
		if offset < 0 || offset >= int64(len(xs)) {
			return core.Null()
		}
		return xs[int(offset)]
	}
	if key, ok := index.AsString(); ok {
		if v, exists := receiver.Lookup(key); exists {
			return v
		}
	}
	return core.Null()
}
func integerIndex(v core.Value) (int64, bool) {
	if i, ok := v.AsInt(); ok {
		return i, true
	}
	if f, ok := v.AsFloat(); ok && f >= -0x1p63 && f < 0x1p63 && math.Trunc(f) == f {
		return int64(f), true
	}
	return 0, false
}
