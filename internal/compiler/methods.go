package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/internal/syntax"
)

func (c *compiler) call(receiver plan.Node, n *syntax.MethodCall, span core.Span) (plan.Node, error) {
	name := syntax.DecodeName(n.Name)
	declarations := builtins(name)
	if len(declarations) == 0 {
		return c.extensionCall(receiver, n, span)
	}
	var compatible []declaration
	for _, d := range declarations {
		if len(n.Args) < d.min || (d.max >= 0 && len(n.Args) > d.max) {
			continue
		}
		for _, branch := range nonNullBranches(receiver.Type()) {
			if receiverMatches(branch, d.receiver) {
				compatible = append(compatible, d)
				break
			}
		}
	}
	if len(compatible) == 0 {
		return plan.Node{}, c.failure(core.TypeError, n, fmt.Sprintf("method %q cannot be called on %s with %d arguments", name, describeType(receiver.Type()), len(n.Args)))
	}
	higher := name == "map" || name == "flatmap" || name == "any" || name == "all" || name == "filter" || (name == "count" || name == "sum" || name == "min" || name == "max") && len(n.Args) == 1 || name == "foldl" || name == "foldr"
	var args []plan.Node
	var callbackResult core.Type
	if higher {
		var err error
		args, callbackResult, err = c.higherOrder(receiver, n)
		if err != nil {
			return plan.Node{}, err
		}
	} else {
		var failures []error
		for _, arg := range n.Args {
			expr, ok := arg.(syntax.Expr)
			if !ok {
				failures = append(failures, c.failure(core.TypeError, arg, "callback is only valid in a higher-order method argument"))
				continue
			}
			value, err := c.expression(expr)
			if err != nil {
				if fatalCompile(err) {
					return plan.Node{}, err
				}
				failures = append(failures, err)
				continue
			}
			args = append(args, value)
		}
		if err := compileErrors(failures...); err != nil {
			return plan.Node{}, err
		}
	}
	var signatures []plan.Signature
	var results []core.Type
	for _, d := range compatible {
		valid := true
		for i, arg := range args {
			index := i
			if d.variadic {
				index = 0
			}
			if index >= len(d.params) || (!higher && !accepted(arg.Type(), d.params[index])) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		var familyResults []core.Type
		for _, branch := range nonNullBranches(receiver.Type()) {
			if !receiverMatches(branch, d.receiver) {
				continue
			}
			typ, applicable := specialize(d, branch, args, callbackResult)
			if applicable {
				familyResults = append(familyResults, typ)
			}
		}
		if len(familyResults) == 0 {
			continue
		}
		if err := c.allow(capabilities.Method(d.family+"."+d.name), n); err != nil {
			return plan.Node{}, err
		}
		switch d.family + "." + d.name {
		case "any.in", "list.contains":
			compared := receiver.Type()
			if d.family == "list" {
				compared = element(compared)
			}
			for _, arg := range args {
				if err := c.equalityCapabilities(compared, arg.Type(), n); err != nil {
					return plan.Node{}, err
				}
			}
		case "list.unique":
			item := element(receiver.Type())
			if err := c.equalityCapabilities(item, item, n); err != nil {
				return plan.Node{}, err
			}
		}
		parameters := d.params
		if higher {
			parameters = make([]core.Type, len(args))
			for i, arg := range args {
				parameters[i] = arg.Type()
			}
			if name == "foldl" || name == "foldr" {
				parameters[0] = callbackResult
			}
		}
		result := union(familyResults...)
		signatures = append(signatures, plan.NewSignature(d.family+"."+d.name, d.receiver, parameters, result, d.variadic))
		results = append(results, result)
	}
	if len(signatures) == 0 {
		var types []string
		for _, arg := range args {
			types = append(types, describeType(arg.Type()))
		}
		return plan.Node{}, c.failure(core.TypeError, n, fmt.Sprintf("method %q on %s does not accept argument types (%s)", name, describeType(receiver.Type()), strings.Join(types, ", ")))
	}
	sort.Slice(signatures, func(i, j int) bool { return signatures[i].ID() < signatures[j].ID() })
	result := union(results...)
	// type()/stringification observe ordinary null, whereas all other receivers
	// propagate it. exists() additionally consumes guard failure as false.
	if name != "type" && !(name == "string" && len(args) == 0) && name != "json" && name != "exists" {
		result = nullable(result)
	}
	children := append([]plan.Node{receiver}, args...)
	may := false
	for _, child := range children {
		may = may || child.MayNoValue()
		if child.Kind() == plan.CallbackNode {
			body, _ := child.Child(0)
			may = may || body.MayNoValue()
		}
	}
	if name == "exists" {
		may = false
	}
	return plan.NewNode(plan.Spec{Kind: plan.CallNode, Type: result, Span: span, Children: children, Method: plan.NewMethod(name, signatures...), MayNoValue: may}), nil
}
