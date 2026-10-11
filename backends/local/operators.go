package local

import (
	"bytes"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"math"
	"regexp"
	"strings"
)

func numeric(v core.Value) bool { return v.Kind() == core.IntKind || v.Kind() == core.FloatKind }
func float(v core.Value) float64 {
	if f, ok := v.AsFloat(); ok {
		return f
	}
	i, _ := v.AsInt()
	return float64(i)
}
func arithmetic(op plan.Operator, a, b core.Value) core.Value {
	if !numeric(a) || !numeric(b) {
		return core.Null()
	}
	if op == plan.Remainder {
		ai, ok := a.AsInt()
		bi, ok2 := b.AsInt()
		if !ok || !ok2 || bi == 0 {
			return core.Null()
		}
		return core.Int(ai % bi)
	}
	if a.Kind() == core.FloatKind || b.Kind() == core.FloatKind || op == plan.Divide {
		x, y := float(a), float(b)
		var value float64
		switch op {
		case plan.Add:
			value = x + y
		case plan.Subtract:
			value = x - y
		case plan.Multiply:
			value = x * y
		case plan.Divide:
			if y == 0 {
				return core.Null()
			}
			value = x / y
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return core.Null()
		}
		return core.Float(value)
	}
	x, _ := a.AsInt()
	y, _ := b.AsInt()
	var z int64
	switch op {
	case plan.Add:
		z = x + y
		if (y > 0 && z < x) || (y < 0 && z > x) {
			return core.Null()
		}
	case plan.Subtract:
		z = x - y
		if (y < 0 && z < x) || (y > 0 && z > x) {
			return core.Null()
		}
	case plan.Multiply:
		if (x == math.MinInt64 && y == -1) || (y == math.MinInt64 && x == -1) {
			return core.Null()
		}
		z = x * y
		if y != 0 && z/y != x {
			return core.Null()
		}
	}
	return core.Int(z)
}
func compareIntFloat(i int64, f float64) int {
	if f >= 0x1p63 {
		return -1
	}
	if f < -0x1p63 {
		return 1
	}
	truncated := math.Trunc(f)
	integer := int64(truncated)
	if i < integer {
		return -1
	}
	if i > integer {
		return 1
	}
	if f > truncated {
		return -1
	}
	if f < truncated {
		return 1
	}
	return 0
}
func compareNumbers(a, b core.Value) int {
	ai, aInt := a.AsInt()
	bi, bInt := b.AsInt()
	if aInt && bInt {
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
		return 0
	}
	if aInt {
		f, _ := b.AsFloat()
		return compareIntFloat(ai, f)
	}
	if bInt {
		f, _ := a.AsFloat()
		return -compareIntFloat(bi, f)
	}
	x, _ := a.AsFloat()
	y, _ := b.AsFloat()
	if x < y {
		return -1
	}
	if x > y {
		return 1
	}
	return 0
}
func (e *evaluation) binary(n plan.Node) (result, error) {
	op, _ := n.Operator()
	arith := op == plan.Add || op == plan.Subtract || op == plan.Multiply || op == plan.Divide || op == plan.Remainder
	a, err := e.child(n, 0)
	if err != nil {
		return result{}, err
	}
	if !a.present || a.value.Kind() == core.NullKind {
		if arith {
			return a, nil
		}
		return ordinary(core.Bool(false)), nil
	}
	b, err := e.child(n, 1)
	if err != nil {
		return result{}, err
	}
	if !b.present || b.value.Kind() == core.NullKind {
		if arith {
			return b, nil
		}
		return ordinary(core.Bool(false)), nil
	}
	if arith {
		return ordinary(arithmetic(op, a.value, b.value)), nil
	}
	if op == plan.Match || op == plan.NotMatch {
		text, ok := a.value.AsString()
		pattern, ok2 := b.value.AsString()
		if !ok || !ok2 {
			return null(), nil
		}
		return e.regex(text, pattern, op == plan.NotMatch)
	}
	if op == plan.Equal || op == plan.NotEqual {
		if a.value.Kind() != b.value.Kind() && !(numeric(a.value) && numeric(b.value)) {
			return null(), nil
		}
		yes, err := e.equal(a.value, b.value)
		if err != nil {
			return result{}, err
		}
		if op == plan.NotEqual {
			yes = !yes
		}
		return ordinary(core.Bool(yes)), nil
	}
	order, valid, err := e.order(a.value, b.value)
	if err != nil {
		return result{}, err
	}
	if !valid {
		return null(), nil
	}
	yes := false
	switch op {
	case plan.Less:
		yes = order < 0
	case plan.LessEqual:
		yes = order <= 0
	case plan.Greater:
		yes = order > 0
	case plan.GreaterEqual:
		yes = order >= 0
	}
	return ordinary(core.Bool(yes)), nil
}
func (e *evaluation) order(a, b core.Value) (int, bool, error) {
	if numeric(a) && numeric(b) {
		return compareNumbers(a, b), true, nil
	}
	if a.Kind() != b.Kind() {
		return 0, false, nil
	}
	switch a.Kind() {
	case core.StringKind:
		x, _ := a.AsString()
		y, _ := b.AsString()
		if err := e.charge(len(x)); err != nil {
			return 0, false, err
		}
		if err := e.charge(len(y)); err != nil {
			return 0, false, err
		}
		return strings.Compare(x, y), true, nil
	case core.TimestampKind:
		x, _ := a.AsTimestamp()
		y, _ := b.AsTimestamp()
		return x.Compare(y), true, nil
	case core.DurationKind:
		x, _ := a.AsDuration()
		y, _ := b.AsDuration()
		if x < y {
			return -1, true, nil
		}
		if x > y {
			return 1, true, nil
		}
		return 0, true, nil
	}
	return 0, false, nil
}
func (e *evaluation) equal(a, b core.Value) (bool, error) {
	if err := e.charge(1); err != nil {
		return false, err
	}
	if numeric(a) && numeric(b) {
		return compareNumbers(a, b) == 0, nil
	}
	if a.Kind() != b.Kind() {
		return false, nil
	}
	switch a.Kind() {
	case core.NullKind:
		return true, nil
	case core.BoolKind:
		x, _ := a.AsBool()
		y, _ := b.AsBool()
		return x == y, nil
	case core.StringKind, core.TimestampKind, core.DurationKind:
		order, _, err := e.order(a, b)
		return order == 0, err
	case core.BytesKind:
		x, _ := a.BytesView()
		y, _ := b.BytesView()
		if err := e.charge(len(x)); err != nil {
			return false, err
		}
		if err := e.charge(len(y)); err != nil {
			return false, err
		}
		return bytes.Equal(x, y), nil
	case core.ListKind:
		xs, _ := a.ListView()
		ys, _ := b.ListView()
		if len(xs) != len(ys) {
			return false, nil
		}
		for i := range xs {
			yes, err := e.equal(xs[i], ys[i])
			if err != nil || !yes {
				return false, err
			}
		}
		return true, nil
	case core.ObjectKind:
		xs, _ := a.ObjectView()
		ys, _ := b.ObjectView()
		if len(xs) != len(ys) {
			return false, nil
		}
		keys, err := e.objectKeys(xs)
		if err != nil {
			return false, err
		}
		for _, k := range keys {
			x := xs[k]
			y, exists := ys[k]
			if !exists {
				return false, nil
			}
			yes, err := e.equal(x, y)
			if err != nil || !yes {
				return false, err
			}
		}
		return true, nil
	}
	return false, nil
}

func (e *evaluation) regex(text, pattern string, invert bool) (result, error) {
	if err := e.charge(len(text)); err != nil {
		return result{}, err
	}
	if err := e.charge(len(pattern)); err != nil {
		return result{}, err
	}
	re, err := regexp.Compile(pattern)
	if contextErr := cancelled(e.ctx); contextErr != nil {
		return result{}, contextErr
	}
	if err != nil {
		return null(), nil
	}
	yes := re.MatchString(text)
	if err := cancelled(e.ctx); err != nil {
		return result{}, err
	}
	if invert {
		yes = !yes
	}
	return ordinary(core.Bool(yes)), nil
}
