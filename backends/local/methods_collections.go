package local

import (
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

func (e *evaluation) collectionMethod(id string, receiver core.Value, args []core.Value) (result, error) {
	switch id {
	case "list.size", "list.count":
		xs, _ := receiver.ListView()
		return ordinary(core.Int(int64(len(xs)))), nil
	case "list.sum":
		xs, _ := receiver.ListView()
		return e.sumList(xs, plan.Node{}, false)
	case "list.min", "list.max":
		xs, _ := receiver.ListView()
		return e.extremeList(xs, plan.Node{}, false, id == "list.min")
	case "object.size":
		obj, _ := receiver.ObjectView()
		return ordinary(core.Int(int64(len(obj)))), nil
	case "list.has":
		xs, _ := receiver.ListView()
		offset, valid := integerIndex(args[0])
		if !valid {
			return null(), nil
		}
		if offset < 0 {
			offset += int64(len(xs))
		}
		return ordinary(core.Bool(offset >= 0 && offset < int64(len(xs)))), nil
	case "object.has", "object.get":
		key, _ := args[0].AsString()
		if err := e.charge(len(key)); err != nil {
			return result{}, err
		}
		value, exists := receiver.Lookup(key)
		if id == "object.has" {
			return ordinary(core.Bool(exists)), nil
		}
		if !exists {
			return null(), nil
		}
		return ordinary(value), nil
	case "list.contains":
		xs, _ := receiver.ListView()
		for _, needle := range args {
			found := false
			for _, value := range xs {
				yes, err := e.equal(value, needle)
				if err != nil {
					return result{}, err
				}
				if yes {
					found = true
					break
				}
			}
			if !found {
				return ordinary(core.Bool(false)), nil
			}
		}
		return ordinary(core.Bool(true)), nil
	case "list.unique":
		xs, _ := receiver.ListView()
		for i, value := range xs {
			for j := 0; j < i; j++ {
				yes, err := e.equal(value, xs[j])
				if err != nil {
					return result{}, err
				}
				if yes {
					return ordinary(core.Bool(false)), nil
				}
			}
		}
		return ordinary(core.Bool(true)), nil
	case "list.concat":
		a, _ := receiver.ListView()
		b, _ := args[0].ListView()
		if err := e.allocate(1); err != nil {
			return result{}, err
		}
		for _, n := range []int{len(a), len(b)} {
			if err := e.charge(n); err != nil {
				return result{}, err
			}
			if err := e.allocate(n); err != nil {
				return result{}, err
			}
		}
		xs := make([]core.Value, 0, len(a)+len(b))
		xs = append(xs, a...)
		xs = append(xs, b...)
		return ordinary(core.OwnedList(xs)), nil
	case "list.sort":
		return e.sortList(receiver)
	case "object.keys", "object.values", "object.items":
		obj, _ := receiver.ObjectView()
		keys, err := e.objectKeys(obj)
		if err != nil {
			return result{}, err
		}
		if err = e.allocate(1); err != nil {
			return result{}, err
		}
		if err = e.allocate(len(keys)); err != nil {
			return result{}, err
		}
		xs := make([]core.Value, 0, len(keys))
		for _, key := range keys {
			if err = e.charge(1); err != nil {
				return result{}, err
			}
			switch id {
			case "object.keys":
				xs = append(xs, core.String(key))
			case "object.values":
				xs = append(xs, obj[key])
			case "object.items":
				if err = e.allocate(3); err != nil {
					return result{}, err
				}
				xs = append(xs, core.OwnedList([]core.Value{core.String(key), obj[key]}))
			}
		}
		return ordinary(core.OwnedList(xs)), nil
	}
	return result{}, &core.Error{Code: core.BackendError, Message: "unsupported collection method " + id}
}

// Stable merge sort owns its output and charges each comparison. No comparator
// can hide cancellation or a resource-limit error inside a sorting callback.
func (e *evaluation) sortList(receiver core.Value) (result, error) {
	source, _ := receiver.ListView()
	for _, value := range source {
		if err := e.charge(1); err != nil {
			return result{}, err
		}
		switch value.Kind() {
		case core.IntKind, core.FloatKind, core.StringKind, core.TimestampKind, core.DurationKind:
		default:
			return null(), nil
		}
		if len(source) > 0 {
			_, valid, err := e.order(source[0], value)
			if err != nil {
				return result{}, err
			}
			if !valid {
				return null(), nil
			}
		}
	}
	if err := e.allocate(1); err != nil {
		return result{}, err
	}
	if err := e.allocate(len(source)); err != nil {
		return result{}, err
	}
	values := append([]core.Value(nil), source...)
	if err := e.charge(len(source)); err != nil {
		return result{}, err
	}
	if len(values) < 2 {
		return ordinary(core.OwnedList(values)), nil
	}
	scratch := make([]core.Value, len(values))
	if err := e.mergeSort(values, scratch); err != nil {
		return result{}, err
	}
	return ordinary(core.OwnedList(values)), nil
}
func (e *evaluation) mergeSort(values, scratch []core.Value) error {
	if len(values) < 2 {
		return nil
	}
	mid := len(values) / 2
	if err := e.mergeSort(values[:mid], scratch[:mid]); err != nil {
		return err
	}
	if err := e.mergeSort(values[mid:], scratch[mid:]); err != nil {
		return err
	}
	i, j, k := 0, mid, 0
	for i < mid && j < len(values) {
		if err := e.charge(1); err != nil {
			return err
		}
		order, _, err := e.order(values[i], values[j])
		if err != nil {
			return err
		}
		if order <= 0 {
			scratch[k] = values[i]
			i++
		} else {
			scratch[k] = values[j]
			j++
		}
		k++
	}
	if err := e.charge(len(values) - k); err != nil {
		return err
	}
	k += copy(scratch[k:], values[i:mid])
	copy(scratch[k:], values[j:])
	if err := e.charge(len(values)); err != nil {
		return err
	}
	copy(values, scratch)
	return nil
}
