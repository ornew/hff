package compiler

import (
	"fmt"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/internal/syntax"
)

func (c *compiler) binary(n *syntax.Binary) (plan.Node, error) {
	op := plan.Operator(n.Op.Text)
	arithmetic := op == plan.Add || op == plan.Subtract || op == plan.Multiply || op == plan.Divide || op == plan.Remainder
	feature := capabilities.Comparison
	if arithmetic {
		feature = capabilities.Arithmetic
	}
	if op == plan.Match || op == plan.NotMatch {
		feature = capabilities.RegexOperators
	}
	if err := c.allow(feature, n); err != nil {
		return plan.Node{}, err
	}
	left, leftErr := c.expression(n.Left)
	if fatalCompile(leftErr) {
		return plan.Node{}, leftErr
	}
	if !arithmetic && (op == plan.Equal || op == plan.NotEqual) {
		if _, ok := strip(n.Right).(*syntax.Null); ok {
			if leftErr != nil {
				return plan.Node{}, leftErr
			}
			return plan.NewNode(plan.Spec{Kind: plan.NullTestNode, Type: primitive(core.BoolType), Operator: op, Span: syntax.ExprSpan(n), Children: []plan.Node{left}}), nil
		}
	}
	right, rightErr := c.expression(n.Right)
	if err := compileErrors(leftErr, rightErr); err != nil {
		return plan.Node{}, err
	}
	valid := bottom(left.Type()) || bottom(right.Type())
	incompatible := false
	var results []core.Type
	for _, l := range branches(left.Type()) {
		for _, r := range branches(right.Type()) {
			if bottom(l) || bottom(r) {
				continue
			}
			applicable := false
			if arithmetic {
				applicable = isNumber(l) && isNumber(r)
				if op == plan.Remainder {
					applicable = l.Kind() == core.IntType && r.Kind() == core.IntType
				}
				if applicable {
					result := primitive(core.IntType)
					if op == plan.Divide || l.Kind() == core.FloatType || r.Kind() == core.FloatType {
						result = primitive(core.FloatType)
					}
					results = append(results, result)
				}
			} else if op == plan.Match || op == plan.NotMatch {
				applicable = l.Kind() == core.StringType && r.Kind() == core.StringType
			} else if op == plan.Equal || op == plan.NotEqual {
				applicable = (isNumber(l) && isNumber(r)) || (isObject(l) && isObject(r)) || l.Kind() == r.Kind()
			} else {
				applicable = (isNumber(l) && isNumber(r)) || (l.Kind() == r.Kind() && (l.Kind() == core.StringType || l.Kind() == core.TimestampType || l.Kind() == core.DurationType))
			}
			valid = valid || applicable
			incompatible = incompatible || !applicable
		}
	}
	if !valid {
		return plan.Node{}, c.failure(core.TypeError, n, fmt.Sprintf("operator %q cannot compare or combine %s and %s", n.Op.Text, describeType(left.Type()), describeType(right.Type())))
	}
	if op == plan.Equal || op == plan.NotEqual {
		if err := c.equalityCapabilities(left.Type(), right.Type(), n); err != nil {
			return plan.Node{}, err
		}
	}
	typ := primitive(core.BoolType)
	may := false
	if arithmetic {
		typ = nullable(union(results...))
		may = left.MayNoValue() || right.MayNoValue()
	} else if incompatible || op == plan.Match || op == plan.NotMatch {
		typ = nullable(typ)
	}
	return plan.NewNode(plan.Spec{Kind: plan.BinaryNode, Type: typ, Operator: op, Span: syntax.ExprSpan(n), Children: []plan.Node{left, right}, MayNoValue: may}), nil
}
func (c *compiler) access(n *syntax.Access) (plan.Node, error) {
	node, err := c.expression(n.Base)
	if err != nil {
		return plan.Node{}, err
	}
	for _, suffix := range n.Parts {
		span := syntax.ExprSpan(suffix)
		span.Start = syntax.ExprSpan(n.Base).Start
		switch p := suffix.(type) {
		case *syntax.FieldAccess:
			if err := c.allow(capabilities.Access, p); err != nil {
				return plan.Node{}, err
			}
			name := syntax.DecodeName(p.Name)
			typ, ok := fieldType(node.Type(), name)
			if !ok {
				return plan.Node{}, c.failure(core.TypeError, p, fmt.Sprintf("field %q is not declared on %s", name, describeType(node.Type())))
			}
			path, static := schemaPath(node)
			if static {
				path = append(path, name)
			}
			node = plan.NewNode(plan.Spec{Kind: plan.FieldNode, Type: typ, Field: name, Path: path, Span: span, Children: []plan.Node{node}, MayNoValue: node.MayNoValue()})
		case *syntax.TypeGuard:
			if err := c.allow(capabilities.TypeGuard, p); err != nil {
				return plan.Node{}, err
			}
			target, err := c.typeExpression(p.Type)
			if err != nil {
				return plan.Node{}, err
			}
			node = plan.NewNode(plan.Spec{Kind: plan.TypeGuardNode, Type: guardedType(node.Type(), target), Constraint: target, Span: span, Children: []plan.Node{node}, MayNoValue: true})

		case *syntax.Guard:
			if err := c.allow(capabilities.Guard, p); err != nil {
				return plan.Node{}, err
			}
			node = plan.NewNode(plan.Spec{Kind: plan.GuardNode, Type: withoutNull(node.Type()), Span: span, Children: []plan.Node{node}, MayNoValue: true})
		case *syntax.IndexAccess:
			if err := c.allow(capabilities.Index, p); err != nil {
				return plan.Node{}, err
			}
			index, err := c.expression(p.Index)
			if err != nil {
				return plan.Node{}, err
			}
			typ, ok := indexType(node.Type(), index)
			if !ok {
				return plan.Node{}, c.failure(core.TypeError, p, "index has no applicable receiver/key types")
			}
			node = plan.NewNode(plan.Spec{Kind: plan.IndexNode, Type: typ, Span: span, Children: []plan.Node{node, index}, MayNoValue: node.MayNoValue() || index.MayNoValue()})
		case *syntax.MethodCall:
			node, err = c.call(node, p, span)
			if err != nil {
				return plan.Node{}, err
			}
		}
	}
	return node, nil
}
func indexType(receiver core.Type, index plan.Node) (core.Type, bool) {
	valid := bottom(receiver) || bottom(index.Type())
	var results []core.Type
	for _, r := range branches(receiver) {
		for _, i := range branches(index.Type()) {
			switch {
			case bottom(r):
				results = append(results, r)
			case bottom(i):
				results = append(results, primitive(core.NullType))
			case r.Kind() == core.ListType && isNumber(i):
				valid = true
				results = append(results, nullable(r.Element()))
			case isObject(r) && i.Kind() == core.StringType:
				valid = true
				result := objectValues(r)
				if value, literal := index.Literal(); literal {
					if name, ok := value.AsString(); ok {
						if typ, known := fieldType(r, name); known {
							result = typ
						} else {
							result = primitive(core.NullType)
						}
					}
				}
				results = append(results, nullable(result))
			default:
				results = append(results, primitive(core.NullType))
			}
		}
	}
	return union(results...), valid
}

func schemaPath(node plan.Node) ([]string, bool) {
	if path, ok := node.Path(); ok {
		return path, true
	}
	if node.Kind() == plan.GuardNode || node.Kind() == plan.TypeGuardNode {
		if child, ok := node.Child(0); ok {
			return schemaPath(child)
		}
	}
	return nil, false
}
