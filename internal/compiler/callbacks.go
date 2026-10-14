package compiler

import (
	"fmt"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/internal/syntax"
)

func (c *compiler) callback(n *syntax.Callback, parameters []core.Type, predicate bool) (plan.Node, error) {
	if err := c.allow(capabilities.Callbacks, n); err != nil {
		return plan.Node{}, err
	}
	if c.callbackDepth > 0 {
		if err := c.allow(capabilities.NestedCallbacks, n); err != nil {
			return plan.Node{}, err
		}
	}
	c.callbackDepth++
	defer func() { c.callbackDepth-- }()
	if !c.config.Unlimited && c.config.Limits.MaxCallbackDepth > 0 && c.callbackDepth > c.config.Limits.MaxCallbackDepth {
		return plan.Node{}, c.failure(core.LimitError, n, "callback nesting depth exceeds limit")
	}
	if len(n.Params) != len(parameters) {
		return plan.Node{}, c.failure(core.TypeError, n, fmt.Sprintf("callback requires %d parameters", len(parameters)))
	}
	parent := c.scope
	s := &scope{parent: parent, bindings: map[string]plan.Binding{}, captures: map[plan.BindingID]plan.Binding{}}
	var bindings []plan.Binding
	for i, p := range n.Params {
		name := syntax.DecodeName(p)
		if _, duplicate := s.bindings[name]; duplicate {
			return plan.Node{}, c.failure(core.TypeError, n, "duplicate callback parameter")
		}
		b := c.newBinding(name, parameters[i])
		s.bindings[name] = b
		bindings = append(bindings, b)
	}
	c.scope = s
	body, err := c.expression(n.Body)
	c.scope = parent
	if err != nil {
		return plan.Node{}, err
	}
	if predicate {
		body = c.boolean(body, syntax.ExprSpan(n.Body))
	}
	typ := core.NewFunctionType(parameters, body.Type())
	return plan.NewNode(plan.Spec{Kind: plan.CallbackNode, Type: typ, Span: syntax.ExprSpan(n), Children: []plan.Node{body}, Parameters: bindings, Captures: sortedCaptures(s)}), nil
}

func (c *compiler) higherOrder(receiver plan.Node, n *syntax.MethodCall) ([]plan.Node, core.Type, error) {
	name := syntax.DecodeName(n.Name)
	fold := name == "foldl" || name == "foldr"
	argc := 1
	if fold {
		argc = 2
	}
	if len(n.Args) != argc {
		return nil, core.Type{}, c.failure(core.TypeError, n, fmt.Sprintf("%s requires %d arguments", name, argc))
	}
	lambda, ok := n.Args[argc-1].(*syntax.Callback)
	if !ok {
		return nil, core.Type{}, c.failure(core.TypeError, n, "higher-order method requires a named callback")
	}
	item := element(receiver.Type())
	if !fold {
		callback, err := c.callback(lambda, []core.Type{item}, name == "any" || name == "all" || name == "count" || name == "filter")
		if err != nil {
			return nil, core.Type{}, err
		}
		result := callback.Type().Result()
		switch name {
		case "any", "all":
			result = primitive(core.BoolType)
		case "count":
			result = primitive(core.IntType)
		case "sum", "min", "max", "avg":
			var valid bool
			if name == "sum" {
				result, valid = aggregateNumericResult(result)
			} else if name == "min" || name == "max" {
				result, valid = aggregateExtremeResult(result)
			} else {
				valid = len(aggregateNumericCandidates(result)) > 0 || bottom(result)
				result = primitive(core.FloatType)
			}
			if !valid {
				return nil, core.Type{}, c.failure(core.TypeError, lambda, name+" callback must have an applicable numeric result")
			}
		case "filter":
			result = core.NewListType(item)
		case "map":
			result = core.NewListType(result)
		case "flatmap":
			hasList := bottom(result)
			var types []core.Type
			for _, branch := range branches(result) {
				if branch.Kind() == core.AnyType {
					hasList = true
					types = append(types, branch)
				}
				if branch.Kind() == core.ListType {
					hasList = true
					types = append(types, branch.Element())
				}
			}
			if !hasList {
				return nil, core.Type{}, c.failure(core.TypeError, lambda, "flatmap callback must have an applicable list result")
			}
			result = nullable(core.NewListType(union(types...)))
		}
		return []plan.Node{callback}, result, nil
	}
	seed, ok := n.Args[0].(syntax.Expr)
	if !ok {
		return nil, core.Type{}, c.failure(core.TypeError, n, "fold initial value cannot be a callback")
	}
	initial, err := c.expression(seed)
	if err != nil {
		return nil, core.Type{}, err
	}
	accumulator := initial.Type()
	var callback plan.Node
	// Finite unions converge quickly. Recursive accumulator shapes cannot be
	// represented by finite Type descriptors, so widen to Any and recheck the body.
	for pass := 0; pass < 4; pass++ {
		parameters := []core.Type{accumulator, item}
		if name == "foldr" {
			parameters[0], parameters[1] = parameters[1], parameters[0]
		}
		callback, err = c.callback(lambda, parameters, false)
		if err != nil {
			return nil, core.Type{}, err
		}
		next := union(initial.Type(), callback.Type().Result())
		if accumulator.Equal(next) {
			return []plan.Node{initial, callback}, next, nil
		}
		accumulator = next
	}
	accumulator = primitive(core.AnyType)
	parameters := []core.Type{accumulator, item}
	if name == "foldr" {
		parameters[0], parameters[1] = parameters[1], parameters[0]
	}
	callback, err = c.callback(lambda, parameters, false)
	if err != nil {
		return nil, core.Type{}, err
	}
	return []plan.Node{initial, callback}, accumulator, nil
}

// A sum with no contributing values is an Int zero. Otherwise its family is
// determined by the values that may survive null and guarded no-value skips.
func aggregateNumericResult(typ core.Type) (core.Type, bool) {
	numeric := aggregateNumericCandidates(typ)
	if len(numeric) == 0 && !bottom(typ) {
		return core.Type{}, false
	}
	numeric = append(numeric, primitive(core.IntType))
	return union(numeric...), true
}

func aggregateExtremeResult(typ core.Type) (core.Type, bool) {
	numeric := aggregateNumericCandidates(typ)
	if len(numeric) == 0 && !bottom(typ) {
		return core.Type{}, false
	}
	if len(numeric) == 0 {
		return primitive(core.NullType), true
	}
	return union(numeric...), true
}

func aggregateNumericCandidates(typ core.Type) []core.Type {
	var numeric []core.Type
	for _, branch := range branches(typ) {
		switch {
		case branch.Kind() == core.AnyType:
			numeric = append(numeric, primitive(core.IntType), primitive(core.FloatType))
		case isNumber(branch):
			numeric = append(numeric, branch)
		}
	}
	return numeric
}
