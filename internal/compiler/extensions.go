package compiler

import (
	"fmt"
	"sort"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/internal/syntax"
)

func CheckMethods(methods []core.MethodDeclaration) error {
	ids := map[string]bool{}
	for i, d := range methods {
		if !d.Valid() {
			return fmt.Errorf("invalid method declaration")
		}
		if ids[d.ID()] {
			return fmt.Errorf("duplicate method ID %q", d.ID())
		}
		ids[d.ID()] = true
		if len(builtins(d.Name())) != 0 || capabilities.Standard().Has(capabilities.Method(d.ID())) {
			return fmt.Errorf("extension %q overrides a built-in method", d.ID())
		}
		for _, other := range methods[:i] {
			if core.MethodsOverlap(d, other) {
				return fmt.Errorf("ambiguous method signatures %q and %q", d.ID(), other.ID())
			}
		}
	}
	return nil
}
func (c *compiler) extensionCall(receiver plan.Node, n *syntax.MethodCall, span core.Span) (plan.Node, error) {
	name := syntax.DecodeName(n.Name)
	var candidates []core.MethodDeclaration
	for _, d := range c.config.Methods {
		if d.Name() != name || len(n.Args) < d.MinArgs() || d.MaxArgs() >= 0 && len(n.Args) > d.MaxArgs() {
			continue
		}
		if bottom(receiver.Type()) || core.TypesOverlap(receiver.Type(), d.Receiver()) {
			candidates = append(candidates, d)
		}
	}
	if len(candidates) == 0 {
		return plan.Node{}, c.failure(core.TypeError, n, fmt.Sprintf("unknown or inapplicable method %q", name))
	}
	args := make([]plan.Node, len(n.Args))
	var failures []error
	for i, arg := range n.Args {
		expr, ok := arg.(syntax.Expr)
		if !ok {
			failures = append(failures, c.failure(core.TypeError, arg, "extension arguments must be values"))
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
		args[i] = value
	}
	if err := compileErrors(failures...); err != nil {
		return plan.Node{}, err
	}
	var signatures []plan.Signature
	var results []core.Type
	for _, d := range candidates {
		valid := true
		for i, arg := range args {
			want, _ := d.Parameter(i)
			if !bottom(arg.Type()) && !core.TypesOverlap(arg.Type(), want) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		if err := c.allow(capabilities.Method(d.ID()), n); err != nil {
			return plan.Node{}, err
		}
		signatures = append(signatures, plan.NewExtensionSignature(d))
		results = append(results, d.Result())
	}
	if len(signatures) == 0 {
		return plan.Node{}, c.failure(core.TypeError, n, "method has no applicable argument types")
	}
	sort.Slice(signatures, func(i, j int) bool { return signatures[i].ID() < signatures[j].ID() })
	children := append([]plan.Node{receiver}, args...)
	may := false
	for _, child := range children {
		may = may || child.MayNoValue()
	}
	return plan.NewNode(plan.Spec{Kind: plan.CallNode, Type: nullable(union(results...)), Span: span, Children: children, Method: plan.NewMethod(name, signatures...), MayNoValue: may}), nil
}
