// Package schema provides field helpers and immutable HFF type descriptors.
package schema

import "github.com/ornew/hff"

type Type = hff.Type
type FieldDef = hff.Field

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

func Field(name string, t Type) FieldDef      { return FieldDef{Name: name, Type: t} }
func String(name string) FieldDef             { return Field(name, hff.PrimitiveType(hff.StringType)) }
func Int(name string) FieldDef                { return Field(name, hff.PrimitiveType(hff.IntType)) }
func Float(name string) FieldDef              { return Field(name, hff.PrimitiveType(hff.FloatType)) }
func Bool(name string) FieldDef               { return Field(name, hff.PrimitiveType(hff.BoolType)) }
func Bytes(name string) FieldDef              { return Field(name, hff.PrimitiveType(hff.BytesType)) }
func Timestamp(name string) FieldDef          { return Field(name, hff.PrimitiveType(hff.TimestampType)) }
func Duration(name string) FieldDef           { return Field(name, hff.PrimitiveType(hff.DurationType)) }
func Any(name string) FieldDef                { return Field(name, hff.PrimitiveType(hff.AnyType)) }
func Struct(fields ...FieldDef) Type          { return hff.NewStructType(fields...) }
func List(name string, element Type) FieldDef { return Field(name, ListOf(element)) }
func Map(name string, element Type) FieldDef  { return Field(name, MapOf(element)) }
func ListOf(element Type) Type                { return hff.NewListType(element) }
func MapOf(element Type) Type                 { return hff.NewMapType(element) }
func Union(members ...Type) Type              { return hff.NewUnionType(members...) }
func Optional(field FieldDef) FieldDef        { field.Optional = true; return field }
func Nullable(field FieldDef) FieldDef        { field.Type = NullableType(field.Type); return field }
func NullableType(t Type) Type                { return Union(t, hff.PrimitiveType(hff.NullType)) }
