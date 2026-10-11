package compiler

import (
	"fmt"

	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/syntax"
)

func (c *compiler) typeExpression(expr syntax.TypeExpr) (core.Type, error) {
	switch n := expr.(type) {
	case *syntax.TypeGroup:
		return c.typeExpression(n.X)
	case *syntax.TypeUnion:
		var types []core.Type
		for _, part := range n.Types {
			typ, err := c.typeExpression(part)
			if err != nil {
				return core.Type{}, err
			}
			types = append(types, typ)
		}
		return union(types...), nil
	case *syntax.TypeNamed:
		name := n.Name.Text
		args := n.Args
		if name == "list" || name == "map" {
			if len(args) == 0 && name == "list" {
				return core.NewListType(primitive(core.AnyType)), nil
			}
			if len(args) != 1 {
				return core.Type{}, c.failure(core.TypeError, n, fmt.Sprintf("%s type requires one element type", name))
			}
			element, err := c.typeExpression(args[0])
			if err != nil {
				return core.Type{}, err
			}
			if name == "list" {
				return core.NewListType(element), nil
			}
			return core.NewMapType(element), nil
		}
		if len(args) > 0 {
			return core.Type{}, c.failure(core.TypeError, n, "only list and map types accept element parameters")
		}
		switch name {
		case "any":
			return primitive(core.AnyType), nil
		case "null":
			return primitive(core.NullType), nil
		case "bool":
			return primitive(core.BoolType), nil
		case "number":
			return union(primitive(core.IntType), primitive(core.FloatType)), nil
		case "int":
			return primitive(core.IntType), nil
		case "float":
			return primitive(core.FloatType), nil
		case "string":
			return primitive(core.StringType), nil
		case "bytes":
			return primitive(core.BytesType), nil
		case "timestamp":
			return primitive(core.TimestampType), nil
		case "duration":
			return primitive(core.DurationType), nil
		case "object":
			return core.NewMapType(primitive(core.AnyType)), nil
		}
		return core.Type{}, c.failure(core.TypeError, n, fmt.Sprintf("unknown type %q", name))
	}
	return core.Type{}, c.failure(core.TypeError, expr, "invalid type expression")
}

// intersection preserves schema information guaranteed by both contracts. Empty
// collections remain possible even when their element contracts are disjoint.
func intersection(actual, target core.Type) core.Type {
	if actual.Kind() == core.AnyType {
		return target
	}
	if target.Kind() == core.AnyType {
		return actual
	}
	var results []core.Type
	for _, a := range branches(actual) {
		for _, t := range branches(target) {
			if a.Kind() == core.ListType && t.Kind() == core.ListType {
				results = append(results, core.NewListType(intersection(a.Element(), t.Element())))
				continue
			}
			if isObject(a) && t.Kind() == core.MapType {
				if a.Kind() == core.MapType {
					results = append(results, core.NewMapType(intersection(a.Element(), t.Element())))
					continue
				}
				var fields []core.Field
				valid := true
				for _, field := range a.Fields() {
					field.Optional = field.Optional || permitsNull(field.Type)
					field.Type = intersection(field.Type, t.Element())
					if field.Type.Kind() == core.NeverType && !field.Optional {
						valid = false
						break
					}
					fields = append(fields, field)
				}
				if valid {
					results = append(results, core.NewStructType(fields...))
				}
				continue
			}
			if a.Kind() == t.Kind() {
				results = append(results, a)
			}
		}
	}
	return union(results...)
}
func guardedType(actual, target core.Type) core.Type {
	narrowed := intersection(actual, target)
	// An explicit nullable target also supplies overload context to a null-only
	// expression. Impossible tests keep the target as a safe upper bound: they
	// produce no value, rather than a value of an unrelated type.
	if narrowed.Kind() == core.NeverType || narrowed.Kind() == core.NullType {
		return target
	}
	return narrowed
}

func permitsNull(t core.Type) bool {
	for _, branch := range branches(t) {
		if branch.Kind() == core.NullType {
			return true
		}
	}
	return false
}
