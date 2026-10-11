package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

// TypeKind includes compiler types that are not host values.
type TypeKind uint8

const (
	InvalidType TypeKind = iota
	NullType
	BoolType
	IntType
	FloatType
	StringType
	BytesType
	ListType
	StructType
	MapType
	TimestampType
	DurationType
	AnyType
	NeverType
	UnionType
	FunctionType // Compiler-only callback signature; never a host schema type.
)

// Type is an opaque immutable schema descriptor. The zero value is invalid.
type Type struct{ node *typeNode }
type typeNode struct {
	kind       TypeKind
	element    Type
	fields     []Field
	members    []Type
	parameters []Type
	result     Type
}

func NewFunctionType(parameters []Type, result Type) Type {
	return Type{&typeNode{kind: FunctionType, parameters: append([]Type(nil), parameters...), result: result}}
}
func (t Type) Parameters() []Type {
	if t.Kind() != FunctionType {
		return nil
	}
	return append([]Type(nil), t.node.parameters...)
}
func (t Type) Result() Type {
	if t.Kind() != FunctionType {
		return Type{}
	}
	return t.node.result
}

// Field describes presence independently from the value's nullable type.
type Field struct {
	Name     string
	Type     Type
	Optional bool
}

func PrimitiveType(k TypeKind) Type {
	switch k {
	case NullType, BoolType, IntType, FloatType, StringType, BytesType, TimestampType, DurationType, AnyType, NeverType:
		return Type{&typeNode{kind: k}}
	default:
		return Type{}
	}
}
func NewListType(element Type) Type { return Type{&typeNode{kind: ListType, element: element}} }
func NewMapType(element Type) Type  { return Type{&typeNode{kind: MapType, element: element}} }
func NewStructType(fields ...Field) Type {
	fields = append([]Field(nil), fields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return Type{&typeNode{kind: StructType, fields: fields}}
}
func NewUnionType(members ...Type) Type {
	var flat []Type
	for _, m := range members {
		if m.Kind() == UnionType {
			flat = append(flat, m.node.members...)
		} else {
			flat = append(flat, m)
		}
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].Key() < flat[j].Key() })
	unique := make([]Type, 0, len(flat))
	for _, m := range flat {
		if len(unique) == 0 || unique[len(unique)-1].Key() != m.Key() {
			unique = append(unique, m)
		}
	}
	if len(unique) == 0 {
		return PrimitiveType(NeverType)
	}
	if len(unique) == 1 {
		return unique[0]
	}
	return Type{&typeNode{kind: UnionType, members: unique}}
}
func (t Type) Kind() TypeKind {
	if t.node == nil {
		return InvalidType
	}
	return t.node.kind
}
func (t Type) Element() Type {
	if t.node == nil {
		return Type{}
	}
	return t.node.element
}
func (t Type) Fields() []Field {
	if t.node == nil {
		return nil
	}
	return append([]Field(nil), t.node.fields...)
}
func (t Type) Members() []Type {
	if t.node == nil {
		return nil
	}
	return append([]Type(nil), t.node.members...)
}

// Key is a canonical structural identity, not a hash or a display name.
func (t Type) Key() string {
	switch t.Kind() {
	case ListType, MapType:
		return fmt.Sprintf("%d[%s]", t.Kind(), t.Element().Key())
	case StructType:
		s := "struct["
		for _, f := range t.node.fields {
			name, _ := json.Marshal(f.Name)
			s += fmt.Sprintf("%s:%t:%s;", name, f.Optional, f.Type.Key())
		}
		return s + "]"
	case UnionType:
		s := "union["
		for _, m := range t.node.members {
			s += m.Key() + ";"
		}
		return s + "]"
	case FunctionType:
		s := "function["
		for _, parameter := range t.node.parameters {
			s += parameter.Key() + ";"
		}
		return s + "]->" + t.node.result.Key()
	default:
		return fmt.Sprintf("%d", t.Kind())
	}
}
func (t Type) Equal(other Type) bool { return t.node == other.node || t.Key() == other.Key() }

// Check rejects invalid descriptors and duplicate fields before engine construction.
func (t Type) Check() error { return t.check(0) }
func (t Type) check(depth int) error {
	if depth > 64 {
		return fmt.Errorf("schema nesting exceeds 64 levels")
	}
	switch t.Kind() {
	case InvalidType:
		return fmt.Errorf("invalid schema type")
	case FunctionType:
		return fmt.Errorf("callbacks are not host schema types")
	case ListType, MapType:
		return t.Element().check(depth + 1)
	case StructType:
		for i, f := range t.node.fields {
			if !utf8.ValidString(f.Name) {
				return fmt.Errorf("field name is not valid UTF-8")
			}
			if i > 0 && t.node.fields[i-1].Name == f.Name {
				return fmt.Errorf("duplicate field %q", f.Name)
			}
			if err := f.Type.check(depth + 1); err != nil {
				return fmt.Errorf("field %q: %w", f.Name, err)
			}
		}
	case UnionType:
		for _, m := range t.node.members {
			if err := m.check(depth + 1); err != nil {
				return err
			}
		}
	}
	return nil
}
