// Package schema provides field helpers and immutable HFF type descriptors.
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

// Field creates a required field with the supplied type.
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

// Any creates a required field that accepts any valid HFF value.
func Any(name string) FieldDef { return Field(name, hff.PrimitiveType(hff.AnyType)) }

// Struct creates a fixed-key object type from field definitions.
func Struct(fields ...FieldDef) Type { return hff.NewStructType(fields...) }

// List creates a field whose values are ordered lists of element type.
func List(name string, element Type) FieldDef { return Field(name, ListOf(element)) }

// Map creates a field whose string keys map to element-type values.
func Map(name string, element Type) FieldDef { return Field(name, MapOf(element)) }

// ListOf creates an ordered list type.
func ListOf(element Type) Type { return hff.NewListType(element) }

// MapOf creates a string-keyed map type.
func MapOf(element Type) Type { return hff.NewMapType(element) }

// Union creates a type accepting any supplied member type.
func Union(members ...Type) Type { return hff.NewUnionType(members...) }

// Optional permits absence of the field; it does not add NullType.
func Optional(field FieldDef) FieldDef { field.Optional = true; return field }

// Nullable permits an explicit null value; it does not permit field absence.
func Nullable(field FieldDef) FieldDef { field.Type = NullableType(field.Type); return field }

// NullableType returns the union of t and explicit NullType.
func NullableType(t Type) Type { return Union(t, hff.PrimitiveType(hff.NullType)) }
