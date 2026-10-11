package local

import (
	"github.com/ornew/hff/internal/core"
	"math/bits"
	"sort"
)

func (e *evaluation) matchesType(v core.Value, t core.Type) (bool, error) {
	if err := e.charge(1); err != nil {
		return false, err
	}
	switch t.Kind() {
	case core.AnyType:
		return true, nil
	case core.NeverType:
		return false, nil
	case core.UnionType:
		for _, branch := range t.Members() {
			yes, err := e.matchesType(v, branch)
			if err != nil || yes {
				return yes, err
			}
		}
		return false, nil
	case core.ListType:
		xs, ok := v.ListView()
		if !ok {
			return false, nil
		}
		for _, x := range xs {
			yes, err := e.matchesType(x, t.Element())
			if err != nil || !yes {
				return false, err
			}
		}
		return true, nil
	case core.StructType:
		values, ok := v.ObjectView()
		if !ok {
			return false, nil
		}
		fields := t.Fields()
		known := make(map[string]bool, len(fields))
		for _, field := range fields {
			known[field.Name] = true
			value, present := values[field.Name]
			if !present {
				if field.Optional || typeAdmitsNull(field.Type) {
					continue
				}
				return false, nil
			}
			yes, err := e.matchesType(value, field.Type)
			if err != nil || !yes {
				return false, err
			}
		}
		keys, err := e.objectKeys(values)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			if !known[key] {
				return false, nil
			}
		}
		return true, nil
	case core.MapType:
		xs, ok := v.ObjectView()
		if !ok {
			return false, nil
		}
		keys, err := e.objectKeys(xs)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			yes, err := e.matchesType(xs[key], t.Element())
			if err != nil || !yes {
				return false, err
			}
		}
		return true, nil
	}
	if int(t.Kind()) >= len(primitiveKinds) {
		return false, nil
	}
	want := primitiveKinds[t.Kind()]
	return want != core.InvalidKind && want == v.Kind(), nil
}

func (e *evaluation) objectKeys(obj map[string]core.Value) ([]string, error) {
	n := len(obj)
	// Reserve deterministic work before copying and sorting scratch keys.
	if err := e.charge(n); err != nil {
		return nil, err
	}
	if n > 1 {
		for pass := 0; pass < bits.Len(uint(n)); pass++ {
			if err := e.charge(n); err != nil {
				return nil, err
			}
		}
	}
	keys := make([]string, 0, n)
	for k := range obj {
		if err := e.charge(len(k)); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

var primitiveKinds = [...]core.Kind{
	core.NullType: core.NullKind, core.BoolType: core.BoolKind,
	core.IntType: core.IntKind, core.FloatType: core.FloatKind,
	core.StringType: core.StringKind, core.BytesType: core.BytesKind,
	core.TimestampType: core.TimestampKind, core.DurationType: core.DurationKind,
}

func typeAdmitsNull(t core.Type) bool {
	if t.Kind() == core.NullType {
		return true
	}
	if t.Kind() == core.UnionType {
		for _, m := range t.Members() {
			if typeAdmitsNull(m) {
				return true
			}
		}
	}
	return false
}
