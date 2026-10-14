package local

import (
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

func higherMethod(id string) bool {
	switch id {
	case "list.any", "list.all", "list.count", "list.filter", "list.sum", "list.min", "list.max", "list.avg", "list.map", "list.flatmap", "list.foldl", "list.foldr":
		return true
	}
	return false
}

func (e *evaluation) invoke(callback plan.Node, values ...core.Value) (result, error) {
	if err := e.charge(1); err != nil {
		return result{}, err
	}
	parameters, ok := callback.ParametersView()
	if !ok || len(parameters) != len(values) || len(parameters) > 2 {
		return result{}, &core.Error{Code: core.BackendError, Message: "invalid callback parameter contract"}
	}
	if e.locals == nil {
		e.locals = make(map[plan.BindingID]core.Value)
	}
	// Binding IDs preserve lexical scope even when parameter names shadow a
	// schema variable or an outer callback. Restore bindings on every exit path.
	var previous [2]core.Value
	var existed [2]bool
	for i, binding := range parameters {
		previous[i], existed[i] = e.locals[binding.ID()]
		e.locals[binding.ID()] = values[i]
	}
	defer func() {
		for i, binding := range parameters {
			if existed[i] {
				e.locals[binding.ID()] = previous[i]
			} else {
				delete(e.locals, binding.ID())
			}
		}
	}()
	body, _ := callback.Child(0)
	return e.eval(body)
}

func (e *evaluation) higher(n plan.Node, receiver core.Value, id string) (result, error) {
	xs, _ := receiver.ListView()
	fold := id == "list.foldl" || id == "list.foldr"
	callbackIndex := 1
	var accumulator result
	if fold {
		callbackIndex = 2
		var err error
		accumulator, err = e.child(n, 1)
		if err != nil || !accumulator.present {
			return accumulator, err
		}
	}
	callback, _ := n.Child(callbackIndex)
	if id == "list.sum" {
		return e.sumList(xs, callback, true)
	}
	if id == "list.avg" {
		return e.avgList(xs, callback, true)
	}
	if id == "list.min" || id == "list.max" {
		return e.extremeList(xs, callback, true, id == "list.min")
	}
	if fold {
		for offset := 0; offset < len(xs); offset++ {
			index := offset
			if id == "list.foldr" {
				index = len(xs) - 1 - offset
			}
			var err error
			if id == "list.foldr" {
				accumulator, err = e.invoke(callback, xs[index], accumulator.value)
			} else {
				accumulator, err = e.invoke(callback, accumulator.value, xs[index])
			}
			if err != nil || !accumulator.present {
				return accumulator, err
			}
		}
		return accumulator, nil
	}
	predicate := id == "list.any" || id == "list.all" || id == "list.count" || id == "list.filter"
	var count int64
	var output []core.Value
	if id == "list.filter" || !predicate {
		if err := e.allocate(1); err != nil {
			return result{}, err
		}
	}
	for _, value := range xs {
		evaluated, err := e.invoke(callback, value)
		if err != nil {
			return result{}, err
		}
		if predicate {
			yes := truth(evaluated)
			if id == "list.any" && yes {
				return ordinary(core.Bool(true)), nil
			}
			if id == "list.all" && !yes {
				return ordinary(core.Bool(false)), nil
			}
			if id == "list.count" && yes {
				count++
			}
			if id == "list.filter" && yes {
				if err := e.allocate(1); err != nil {
					return result{}, err
				}
				output = append(output, value)
			}
			continue
		}
		if !evaluated.present {
			return evaluated, nil
		}
		if id == "list.flatmap" {
			values, ok := evaluated.value.ListView()
			if !ok {
				return null(), nil
			}
			if err := e.allocate(len(values)); err != nil {
				return result{}, err
			}
			if err := e.charge(len(values)); err != nil {
				return result{}, err
			}
			output = append(output, values...)
		} else {
			if err := e.allocate(1); err != nil {
				return result{}, err
			}
			output = append(output, evaluated.value)
		}
	}
	if predicate {
		if id == "list.count" {
			return ordinary(core.Int(count)), nil
		}
		if id == "list.filter" {
			return ordinary(core.OwnedList(output)), nil
		}
		return ordinary(core.Bool(id == "list.all")), nil
	}
	return ordinary(core.OwnedList(output)), nil
}
