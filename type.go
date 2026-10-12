package hff

import "github.com/ornew/hff/internal/core"

// TypeKind identifies a schema or compiler type category. FunctionType and
// NeverType are primarily used in compiled plans, not host input schemas.
type TypeKind = core.TypeKind

// Type is an immutable description of values accepted by a schema or produced
// by a compiled expression. Its zero value is invalid; construct descriptors
// with PrimitiveType or the composite constructors, then call Check before
// using one as a host input schema. Function types are compiler metadata and
// are rejected by Check.
type Type core.Type

// Field describes one exact object key in a struct type. Optional marks the
// field as absence-capable without adding NullType. Add NullType to Type to
// permit a present null value; input validation also accepts absence for a
// nullable field. The flag and value type remain distinct parts of the schema.
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
// NullType, and NeverType. Invalid or composite kinds produce the invalid zero
// Type. AnyType accepts any valid HFF value; NeverType accepts no values and
// is mainly useful in compiler analysis. Use the composite constructors for
// list, map, struct, and union descriptors.
func PrimitiveType(k TypeKind) Type { return Type(core.PrimitiveType(k)) }

// NewListType constructs an ordered list type whose elements must match
// element. Check the resulting descriptor before using it as an input schema.
func NewListType(element Type) Type { return Type(core.NewListType(core.Type(element))) }

// NewMapType constructs an object type whose values must all match element.
// Map keys are strings. Check the resulting descriptor before using it as an
// input schema.
func NewMapType(element Type) Type { return Type(core.NewMapType(core.Type(element))) }

// NewStructType constructs a fixed-key object type from fields. Field order is
// canonicalized, but names and descriptors are not repaired: Check reports
// invalid UTF-8 or duplicate names, invalid types, and unsupported function
// types. Empty field names are allowed.
// Optional records key-absence permission independently of the value type. A
// nullable field can use Type: NewUnionType(PrimitiveType(StringType),
// PrimitiveType(NullType)); input validation accepts its absence as a normal
// null-safe case even when Optional is false. Optional still does not add
// NullType to Type.
func NewStructType(fields ...Field) Type {
	out := make([]core.Field, len(fields))
	for i, f := range fields {
		out[i] = core.Field{Name: f.Name, Type: core.Type(f.Type), Optional: f.Optional}
	}
	return Type(core.NewStructType(out...))
}

// NewUnionType constructs a normalized union from member descriptors. Nested
// unions are flattened, duplicate members are removed, and members are sorted
// canonically. No members produces NeverType; one member returns that member
// directly. Include NullType to allow explicit null; this does not make a
// struct field optional.
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

// Element returns the declared element type for a list or map. It returns an
// invalid zero Type for other kinds or an invalid receiver.
func (t Type) Element() Type { return Type(core.Type(t).Element()) }

// Fields returns a defensive copy of a struct's fields. It returns nil for
// non-struct or invalid types; changing the returned slice does not change t.
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

// Members returns a defensive copy of union members, or nil when t is not a
// union. A normalized union has at least two members; singleton unions have
// already collapsed to their member type.
func (t Type) Members() []Type { return publicTypes(core.Type(t).Members()) }

// Parameters returns a defensive copy of function parameter types, or nil for
// a non-function type. Function types are compiler metadata, not host schema
// types accepted by Check.
func (t Type) Parameters() []Type { return publicTypes(core.Type(t).Parameters()) }

// Result returns a function's result type, or an invalid zero Type for other
// kinds or an invalid receiver.
func (t Type) Result() Type { return Type(core.Type(t).Result()) }

// Key returns the stable canonical structural key for descriptor comparison
// and caches. It is not a display format or serialization contract.
func (t Type) Key() string { return core.Type(t).Key() }

// Equal reports structural equality, including field names, optionality, and
// nested member types. It does not infer compatibility or perform coercion.
func (t Type) Equal(other Type) bool { return core.Type(t).Equal(core.Type(other)) }

// Check recursively validates a host-schema descriptor. It rejects invalid
// and function types, duplicate struct keys, and nesting beyond the supported
// schema depth. Engine input schemas additionally require a struct or map root.
func (t Type) Check() error { return core.Type(t).Check() }
