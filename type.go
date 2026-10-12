package hff

import "github.com/ornew/hff/internal/core"

// TypeKind identifies a schema or compiler type category. FunctionType and
// NeverType are primarily used in compiled plans, not host input schemas.
type TypeKind = core.TypeKind

// Type is an immutable type descriptor. Its zero value is invalid.
type Type core.Type

// Field describes a struct field's type and whether absence is permitted.
type Field struct {
	// Name is the exact HFF object key.
	Name string
	// Type is the required value type when the field is present.
	Type Type
	// Optional permits the key to be absent; it does not permit explicit null.
	Optional bool
}

const (
	// InvalidType is the zero and invalid descriptor kind.
	InvalidType = core.InvalidType
	// NullType is the explicit null value type.
	NullType = core.NullType
	// BoolType is the Boolean value type.
	BoolType = core.BoolType
	// IntType is the signed int64 value type.
	IntType = core.IntType
	// FloatType is the finite float64 value type.
	FloatType = core.FloatType
	// StringType is the UTF-8 string value type.
	StringType = core.StringType
	// BytesType is the byte-sequence value type.
	BytesType = core.BytesType
	// ListType is an ordered sequence with one declared element type.
	ListType = core.ListType
	// StructType is a fixed-key object with declared fields.
	StructType = core.StructType
	// MapType is an object whose keys share one declared value type.
	MapType = core.MapType
	// TimestampType is an instant with nanosecond precision.
	TimestampType = core.TimestampType
	// DurationType is a signed duration with nanosecond precision.
	DurationType = core.DurationType
	// AnyType accepts any valid HFF value.
	AnyType = core.AnyType
	// NeverType has no values and is used for compiler analysis.
	NeverType = core.NeverType
	// UnionType accepts a value belonging to any listed member type.
	UnionType = core.UnionType
	// FunctionType describes callback functions in compiled plans.
	FunctionType = core.FunctionType
)

// PrimitiveType constructs a primitive descriptor, including AnyType,
// NullType, and NeverType. Use the composite constructors for list, map,
// struct, and union descriptors.
func PrimitiveType(k TypeKind) Type { return Type(core.PrimitiveType(k)) }

// NewListType constructs a list type with element as its declared value type.
func NewListType(element Type) Type { return Type(core.NewListType(core.Type(element))) }

// NewMapType constructs a map type with element as every value's declared type.
func NewMapType(element Type) Type { return Type(core.NewMapType(core.Type(element))) }

// NewStructType constructs a fixed-key object type. Check validates field names
// and descriptors; duplicate or invalid fields are not silently normalized.
func NewStructType(fields ...Field) Type {
	out := make([]core.Field, len(fields))
	for i, f := range fields {
		out[i] = core.Field{Name: f.Name, Type: core.Type(f.Type), Optional: f.Optional}
	}
	return Type(core.NewStructType(out...))
}

// NewUnionType constructs a normalized union from its member descriptors.
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

// Kind returns the descriptor kind; an invalid zero Type reports InvalidType.
func (t Type) Kind() TypeKind { return core.Type(t).Kind() }

// Element returns the list or map element type, or an invalid Type otherwise.
func (t Type) Element() Type { return Type(core.Type(t).Element()) }

// Fields returns a copy of a struct's fields, or nil for other kinds.
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

// Members returns a defensive copy of union members.
func (t Type) Members() []Type { return publicTypes(core.Type(t).Members()) }

// Parameters returns a defensive copy of function parameter types.
func (t Type) Parameters() []Type { return publicTypes(core.Type(t).Parameters()) }

// Result returns a function result type, or an invalid Type for other kinds.
func (t Type) Result() Type { return Type(core.Type(t).Result()) }

// Key returns the stable canonical key for this type descriptor.
func (t Type) Key() string { return core.Type(t).Key() }

// Equal reports structural type equality.
func (t Type) Equal(other Type) bool { return core.Type(t).Equal(core.Type(other)) }

// Check validates a host-schema descriptor and rejects function types. Engine
// input schemas additionally require a struct or map root.
func (t Type) Check() error { return core.Type(t).Check() }
