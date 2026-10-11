package hff

import "github.com/ornew/hff/internal/core"

// TypeKind includes compiler-only kinds, such as FunctionType and NeverType.
type TypeKind = core.TypeKind

// Type is an immutable type descriptor. Its zero value is invalid.
type Type core.Type

// Field describes a struct field's type and whether absence is permitted.
type Field struct {
	Name     string
	Type     Type
	Optional bool
}

const (
	InvalidType   = core.InvalidType
	NullType      = core.NullType
	BoolType      = core.BoolType
	IntType       = core.IntType
	FloatType     = core.FloatType
	StringType    = core.StringType
	BytesType     = core.BytesType
	ListType      = core.ListType
	StructType    = core.StructType
	MapType       = core.MapType
	TimestampType = core.TimestampType
	DurationType  = core.DurationType
	AnyType       = core.AnyType
	NeverType     = core.NeverType
	UnionType     = core.UnionType
	FunctionType  = core.FunctionType
)

func PrimitiveType(k TypeKind) Type { return Type(core.PrimitiveType(k)) }
func NewListType(element Type) Type { return Type(core.NewListType(core.Type(element))) }
func NewMapType(element Type) Type  { return Type(core.NewMapType(core.Type(element))) }
func NewStructType(fields ...Field) Type {
	out := make([]core.Field, len(fields))
	for i, f := range fields {
		out[i] = core.Field{Name: f.Name, Type: core.Type(f.Type), Optional: f.Optional}
	}
	return Type(core.NewStructType(out...))
}
func NewUnionType(members ...Type) Type { return Type(core.NewUnionType(internalTypes(members)...)) }
func internalTypes(types []Type) []core.Type {
	out := make([]core.Type, len(types))
	for i, t := range types {
		out[i] = core.Type(t)
	}
	return out
}
func publicTypes(types []core.Type) []Type {
	if types == nil {
		return nil
	}
	out := make([]Type, len(types))
	for i, t := range types {
		out[i] = Type(t)
	}
	return out
}
func (t Type) Kind() TypeKind { return core.Type(t).Kind() }
func (t Type) Element() Type  { return Type(core.Type(t).Element()) }
func (t Type) Fields() []Field {
	fields := core.Type(t).Fields()
	if fields == nil {
		return nil
	}
	out := make([]Field, len(fields))
	for i, f := range fields {
		out[i] = Field{Name: f.Name, Type: Type(f.Type), Optional: f.Optional}
	}
	return out
}
func (t Type) Members() []Type       { return publicTypes(core.Type(t).Members()) }
func (t Type) Parameters() []Type    { return publicTypes(core.Type(t).Parameters()) }
func (t Type) Result() Type          { return Type(core.Type(t).Result()) }
func (t Type) Key() string           { return core.Type(t).Key() }
func (t Type) Equal(other Type) bool { return core.Type(t).Equal(core.Type(other)) }
func (t Type) Check() error          { return core.Type(t).Check() }
