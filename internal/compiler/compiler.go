// Package compiler performs common semantic checking before backend construction.
package compiler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/internal/syntax"
)

type Config struct {
	Schema       core.Type
	Capabilities capabilities.Set
	Limits       core.Limits
	Unlimited    bool
	Methods      []core.MethodDeclaration
}
type compiler struct {
	ctx           context.Context
	source        string
	config        Config
	next          plan.BindingID
	steps         int
	roots         map[string]plan.Binding
	scope         *scope
	callbackDepth int
}
type scope struct {
	parent   *scope
	bindings map[string]plan.Binding
	captures map[plan.BindingID]plan.Binding
}

func Compile(ctx context.Context, source string, config Config) (plan.Program, error) {
	tree, err := syntax.ParseQuery(ctx, source, syntax.Config{Limits: config.Limits, Unlimited: config.Unlimited})
	if err != nil {
		return plan.Program{}, err
	}
	c := compiler{ctx: ctx, source: source, config: config, roots: make(map[string]plan.Binding)}
	if err := c.checkFilter(tree); err != nil {
		return plan.Program{}, err
	}
	root, err := c.expression(tree)
	if err != nil {
		return plan.Program{}, err
	}
	root = c.boolean(root, syntax.ExprSpan(tree))
	return plan.NewProgram(source, root, config.Schema, config.Capabilities, config.Limits, config.Unlimited), nil
}
func (c *compiler) failure(code core.ErrorCode, node any, message string) error {
	span := syntax.ExprSpan(node)
	start := span.Start
	line := strings.Count(c.source[:start], "\n") + 1
	lineStart := strings.LastIndexByte(c.source[:start], '\n') + 1
	return &core.Error{Code: code, Span: &span, Line: line, Column: len([]rune(c.source[lineStart:start])) + 1, Message: message}
}
func (c *compiler) allow(id capabilities.ID, node any) error {
	if !c.config.Capabilities.Has(id) {
		return c.failure(core.CapabilityError, node, fmt.Sprintf("feature %s is disabled", id))
	}
	return nil
}
func (c *compiler) node(kind plan.NodeKind, typ core.Type, source any, children ...plan.Node) plan.Node {
	may := false
	for _, child := range children {
		may = may || child.MayNoValue()
	}
	switch kind {
	case plan.BoolNode, plan.AndNode, plan.OrNode, plan.NotNode, plan.NullTestNode:
		may = false
	}
	return plan.NewNode(plan.Spec{Kind: kind, Type: typ, Span: syntax.ExprSpan(source), Children: children, MayNoValue: may})
}
func (c *compiler) boolean(node plan.Node, span core.Span) plan.Node {
	if node.Type().Kind() == core.BoolType && !node.MayNoValue() {
		return node
	}
	return plan.NewNode(plan.Spec{Kind: plan.BoolNode, Type: primitive(core.BoolType), Span: span, Children: []plan.Node{node}})
}
func strip(expr syntax.Expr) syntax.Expr {
	for {
		switch n := expr.(type) {
		case *syntax.Or:
			if len(n.Terms) == 1 {
				expr = n.Terms[0]
				continue
			}
		case *syntax.And:
			if len(n.Terms) == 1 {
				expr = n.Terms[0]
				continue
			}
		case *syntax.Access:
			if len(n.Parts) == 0 {
				expr = n.Base
				continue
			}
		case *syntax.Group:
			expr = n.X
			continue
		}
		return expr
	}
}
func (c *compiler) checkFilter(expr syntax.Expr) error {
	switch n := strip(expr).(type) {
	case *syntax.Empty:
		return nil
	case *syntax.Or:
		for _, x := range n.Terms {
			if err := c.checkFilter(x); err != nil {
				return err
			}
		}
		return nil
	case *syntax.And:
		for _, x := range n.Terms {
			if err := c.checkFilter(x); err != nil {
				return err
			}
		}
		return nil
	case *syntax.Not:
		return c.checkFilter(n.X)
	case *syntax.Binary:
		switch n.Op.Text {
		case "=", "!=", "<", "<=", ">", ">=", "~", "!~":
			if !depends(n.Left) {
				return c.failure(core.TypeError, n.Left, "comparison left operand must depend on a schema variable")
			}
			return nil
		}
	}
	if !depends(expr) {
		return c.failure(core.TypeError, expr, "filter predicate must depend on a schema variable")
	}
	return nil
}
func depends(expr syntax.Expr) bool { return dependencies(expr, nil) }
func dependencies(expr syntax.Expr, locals map[string]bool) bool {
	switch n := expr.(type) {
	case *syntax.Variable:
		return !locals[syntax.DecodeName(n.Name)]
	case *syntax.Or:
		for _, x := range n.Terms {
			if dependencies(x, locals) {
				return true
			}
		}
	case *syntax.And:
		for _, x := range n.Terms {
			if dependencies(x, locals) {
				return true
			}
		}
	case *syntax.Not:
		return dependencies(n.X, locals)
	case *syntax.Unary:
		return dependencies(n.X, locals)
	case *syntax.Binary:
		return dependencies(n.Left, locals) || dependencies(n.Right, locals)
	case *syntax.Group:
		return dependencies(n.X, locals)
	case *syntax.Access:
		if dependencies(n.Base, locals) {
			return true
		}
		for _, part := range n.Parts {
			switch p := part.(type) {
			case *syntax.IndexAccess:
				if dependencies(p.Index, locals) {
					return true
				}
			case *syntax.MethodCall:
				for _, a := range p.Args {
					switch arg := a.(type) {
					case syntax.Expr:
						if dependencies(arg, locals) {
							return true
						}
					case *syntax.Callback:
						nested := make(map[string]bool, len(locals)+len(arg.Params))
						for name, local := range locals {
							nested[name] = local
						}
						for _, param := range arg.Params {
							nested[syntax.DecodeName(param)] = true
						}
						if dependencies(arg.Body, nested) {
							return true
						}
					}
				}
			}
		}
	case *syntax.ListLiteral:
		for _, x := range n.Values {
			if dependencies(x, locals) {
				return true
			}
		}
	case *syntax.ObjectLiteral:
		for _, m := range n.Members {
			if dependencies(m.Value, locals) {
				return true
			}
		}
	}
	return false
}
func (c *compiler) newBinding(name string, t core.Type) plan.Binding {
	c.next++
	return plan.NewBinding(c.next, name, t)
}
func (c *compiler) reference(n *syntax.Variable) (plan.Node, error) {
	name := syntax.DecodeName(n.Name)
	for owner := c.scope; owner != nil; owner = owner.parent {
		if b, ok := owner.bindings[name]; ok {
			for s := c.scope; s != owner; s = s.parent {
				s.captures[b.ID()] = b
			}
			return plan.NewNode(plan.Spec{Kind: plan.ParameterNode, Type: b.Type(), Span: syntax.ExprSpan(n), Binding: b}), nil
		}
	}
	b, ok := c.roots[name]
	if !ok {
		typ, exists := fieldType(c.config.Schema, name)
		if !exists {
			return plan.Node{}, c.failure(core.TypeError, n, fmt.Sprintf("unknown variable %q", name))
		}
		b = c.newBinding(name, typ)
		c.roots[name] = b
	}
	for s := c.scope; s != nil; s = s.parent {
		s.captures[b.ID()] = b
	}
	return plan.NewNode(plan.Spec{Kind: plan.VariableNode, Type: b.Type(), Span: syntax.ExprSpan(n), Binding: b, Path: []string{name}}), nil
}
func (c *compiler) chargeCompile(source any) error {
	if err := core.CheckContext(c.ctx); err != nil {
		return err
	}
	c.steps++
	if !c.config.Unlimited && c.config.Limits.MaxCompileSteps > 0 && c.steps > c.config.Limits.MaxCompileSteps {
		return c.failure(core.LimitError, source, "compilation step limit exceeded")
	}
	return nil
}
func (c *compiler) expression(expr syntax.Expr) (plan.Node, error) {
	if err := c.chargeCompile(expr); err != nil {
		return plan.Node{}, err
	}
	switch n := expr.(type) {
	case *syntax.Empty:
		return c.node(plan.MatchAllNode, primitive(core.BoolType), n), nil
	case *syntax.Or:
		return c.logical(plan.OrNode, n, n.Terms)
	case *syntax.And:
		return c.logical(plan.AndNode, n, n.Terms)
	case *syntax.Not:
		if err := c.allow(capabilities.Logical, n); err != nil {
			return plan.Node{}, err
		}
		child, err := c.expression(n.X)
		if err != nil {
			return plan.Node{}, err
		}
		return c.node(plan.NotNode, primitive(core.BoolType), n, c.boolean(child, syntax.ExprSpan(n.X))), nil
	case *syntax.Group:
		return c.expression(n.X)
	case *syntax.Variable:
		return c.reference(n)
	case *syntax.Number:
		return c.number(n, false, n), nil
	case *syntax.String:
		value, err := syntax.DecodeString(n.Text)
		if err != nil {
			return plan.Node{}, c.failure(core.SyntaxError, n, err.Error())
		}
		return c.literal(core.String(value), primitive(core.StringType), n), nil
	case *syntax.Bool:
		return c.literal(core.Bool(n.Text == "true"), primitive(core.BoolType), n), nil
	case *syntax.Null:
		return c.literal(core.Null(), primitive(core.NullType), n), nil
	case *syntax.Unary:
		if err := c.allow(capabilities.Arithmetic, n); err != nil {
			return plan.Node{}, err
		}
		if value, ok := bareNumber(n.X); ok {
			return c.number(value, true, n), nil
		}
		child, err := c.expression(n.X)
		if err != nil {
			return plan.Node{}, err
		}
		var results []core.Type
		valid := bottom(child.Type())
		for _, t := range branches(child.Type()) {
			if isNumber(t) {
				valid = true
				results = append(results, t)
			}
		}
		if !valid {
			return plan.Node{}, c.failure(core.TypeError, n, "negation requires a number")
		}
		return c.node(plan.NegateNode, nullable(union(results...)), n, child), nil
	case *syntax.Binary:
		return c.binary(n)
	case *syntax.Access:
		return c.access(n)
	case *syntax.ListLiteral:
		if err := c.allow(capabilities.Collections, n); err != nil {
			return plan.Node{}, err
		}
		var children []plan.Node
		var types []core.Type
		for _, x := range n.Values {
			child, err := c.expression(x)
			if err != nil {
				return plan.Node{}, err
			}
			children = append(children, child)
			types = append(types, child.Type())
		}
		return c.node(plan.ListNode, core.NewListType(union(types...)), n, children...), nil
	case *syntax.ObjectLiteral:
		if err := c.allow(capabilities.Collections, n); err != nil {
			return plan.Node{}, err
		}
		var children []plan.Node
		var fields []core.Field
		var keys []string
		seen := map[string]bool{}
		for _, m := range n.Members {
			key, _ := syntax.DecodeString(m.Key.Text)
			if seen[key] {
				return plan.Node{}, c.failure(core.TypeError, &m.Key, "duplicate object key")
			}
			seen[key] = true
			child, err := c.expression(m.Value)
			if err != nil {
				return plan.Node{}, err
			}
			children = append(children, child)
			fields = append(fields, core.Field{Name: key, Type: child.Type()})
			keys = append(keys, key)
		}
		may := false
		for _, child := range children {
			may = may || child.MayNoValue()
		}
		return plan.NewNode(plan.Spec{Kind: plan.ObjectNode, Type: core.NewStructType(fields...), Span: syntax.ExprSpan(n), Children: children, Keys: keys, MayNoValue: may}), nil
	}
	return plan.Node{}, c.failure(core.TypeError, expr, "unsupported expression")
}
func bareNumber(expr syntax.Expr) (*syntax.Number, bool) {
	for {
		switch n := expr.(type) {
		case *syntax.Access:
			if len(n.Parts) == 0 {
				expr = n.Base
				continue
			}
		case *syntax.Number:
			return n, true
		}
		return nil, false
	}
}
func (c *compiler) literal(value core.Value, typ core.Type, source any) plan.Node {
	return plan.NewNode(plan.Spec{Kind: plan.LiteralNode, Type: typ, Literal: value, Span: syntax.ExprSpan(source)})
}
func (c *compiler) number(n *syntax.Number, negative bool, source any) plan.Node {
	text := n.Text
	if negative {
		text = "-" + text
	}
	if strings.ContainsAny(text, ".eE") {
		v, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return c.literal(core.Null(), primitive(core.NullType), source)
		}
		return c.literal(core.Float(v), primitive(core.FloatType), source)
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return c.literal(core.Null(), primitive(core.NullType), source)
	}
	return c.literal(core.Int(v), primitive(core.IntType), source)
}
func (c *compiler) logical(kind plan.NodeKind, source any, terms []syntax.Expr) (plan.Node, error) {
	if len(terms) == 1 {
		return c.expression(terms[0])
	}
	if err := c.allow(capabilities.Logical, source); err != nil {
		return plan.Node{}, err
	}
	var children []plan.Node
	for _, x := range terms {
		child, err := c.expression(x)
		if err != nil {
			return plan.Node{}, err
		}
		children = append(children, c.boolean(child, syntax.ExprSpan(x)))
	}
	return c.node(kind, primitive(core.BoolType), source, children...), nil
}
func sortedCaptures(s *scope) []plan.Binding {
	values := make([]plan.Binding, 0, len(s.captures))
	for _, b := range s.captures {
		values = append(values, b)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID() < values[j].ID() })
	return values
}
