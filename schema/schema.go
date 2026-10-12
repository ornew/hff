package schema

import "github.com/ornew/hff"

// Type is an immutable HFF type descriptor.
type Type = hff.Type

// FieldDef describes a struct field; Optional and Nullable distinguish absence
// from explicit null.
type FieldDef = hff.Field

// Primitive type descriptors are useful as element and union types.
var (
	NullType      = hff.PrimitiveType(hff.NullType)
	BoolType      = hff.PrimitiveType(hff.BoolType)
	IntType       = hff.PrimitiveType(hff.IntType)
	FloatType     = hff.PrimitiveType(hff.FloatType)
	StringType    = hff.PrimitiveType(hff.StringType)
	BytesType     = hff.PrimitiveType(hff.BytesType)
	TimestampType = hff.PrimitiveType(hff.TimestampType)
	DurationType  = hff.PrimitiveType(hff.DurationType)
	AnyType       = hff.PrimitiveType(hff.AnyType)
	NeverType     = hff.PrimitiveType(hff.NeverType)
)

// Field creates a field with the supplied type and Optional=false. Use Optional
// to set the independent optional marker and Nullable to add NullType. Input
// validation accepts a missing nullable field as well as a missing Optional
// field; a present null still requires a nullable type.
func Field(name string, t Type) FieldDef { return FieldDef{Name: name, Type: t} }

// String creates a required string field.
func String(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.StringType)) }

// Int creates a required int64 field.
func Int(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.IntType)) }

// Float creates a required finite float64 field.
func Float(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.FloatType)) }

// Bool creates a required Boolean field.
func Bool(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.BoolType)) }

// Bytes creates a required bytes field.
func Bytes(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.BytesType)) }

// Timestamp creates a required timestamp field.
func Timestamp(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.TimestampType)) }

// Duration creates a required duration field.
func Duration(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.DurationType)) }

// Any creates a required field that accepts any valid HFF value, including
// explicit null. Use Optional(Any(name)) to also permit an absent key.
func Any(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.AnyType)) }

// Struct creates a fixed-key object type from field definitions. For example:
//
//	person := schema.Struct(
//	    schema.String("name"),
//	    schema.Optional(schema.Int("age")),
//	    schema.Nullable(schema.String("nickname")),
//	)
//
// This requires name, marks age optional, and allows nickname to be null. Input
// validation also accepts a missing nullable nickname as a normal absence
// case. Optional remains an independent schema marker. The engine validates
// the resulting descriptor when configured.
func Struct(fields ...FieldDef) Type { return hff.NewStructType(fields...) }

// List creates a required field whose value is an ordered list of element
// type. Use Nullable(List(...)) to permit a null list, or Optional(List(...))
// to permit the field to be absent.
func List(name string, element Type) FieldDef { return Field(name, ListOf(element)) }

// Map creates a required field whose string keys map to element-type values.
// Map keys are strings; Optional and Nullable wrap the field in the same way as
// other field helpers.
func Map(name string, element Type) FieldDef { return Field(name, MapOf(element)) }

// ListOf creates an ordered list type. The list preserves element order and
// every element must satisfy element; call Type.Check before using a
// user-assembled descriptor as an input schema.
func ListOf(element Type) Type { return hff.NewListType(element) }

// MapOf creates a string-keyed map type. Each present value must satisfy
// element; a missing key is distinct from a key whose value is NullType.
func MapOf(element Type) Type { return hff.NewMapType(element) }

// Union creates a normalized type accepting a value of any supplied member
// type. For example, Union(IntType, FloatType) accepts either numeric
// representation; add NullType to allow explicit null. Union does not make a
// struct field optional.
func Union(members ...Type) Type { return hff.NewUnionType(members...) }

// Optional sets the field's independent optional marker; it does not add
// NullType. A nullable field also accepts absence during input validation, but
// Optional records absence separately in the schema. Use Optional(Nullable(f))
// when both schema markers are wanted.
func Optional(field FieldDef) FieldDef { field.Optional = true; return field }

// Nullable permits an explicit null value by adding NullType to the field's
// value type. It does not set the Optional marker; input validation nevertheless
// accepts a missing nullable field as a normal null-safe absence case.
func Nullable(field FieldDef) FieldDef { field.Type = NullableType(field.Type); return field }

// NullableType returns the union of t and explicit NullType. Use it when a
// present field may contain null. Input validation also accepts an absent
// nullable field; FieldDef.Optional is a separate schema marker and is not set
// by this helper.
func NullableType(t Type) Type { return Union(t, hff.PrimitiveType(hff.NullType)) }
