package sql

import (
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
)

// comparisonShape merges types at the same stored JSON position. List
// alternatives share an element type, and Map alternatives contribute to
// every declared Struct key as well as undeclared keys.
type comparisonShape struct {
	members                []hff.Type
	any, integer, floating bool
	list, object           bool
	element                hff.Type
	keys                   []string
	fields                 map[string]hff.Type
	fallback               hff.Type
}

func makeComparisonShape(typ hff.Type) comparisonShape {
	s := comparisonShape{members: []hff.Type{typ}}
	if typ.Kind() == hff.UnionType {
		s.members = typ.Members()
	}
	for _, member := range s.members {
		if member.Kind() == hff.AnyType {
			s.any = true
			return s
		}
	}
	s.integer, s.floating = jsonNumericCandidates(typ)
	var elements, fallback []hff.Type
	fields := make(map[string][]hff.Type)
	for _, member := range s.members {
		switch member.Kind() {
		case hff.ListType:
			s.list = true
			elements = append(elements, member.Element())
		case hff.MapType:
			s.object = true
			fallback = append(fallback, member.Element())
		case hff.StructType:
			s.object = true
			for _, field := range member.Fields() {
				if _, exists := fields[field.Name]; !exists {
					s.keys = append(s.keys, field.Name)
				}
				fields[field.Name] = append(fields[field.Name], field.Type)
			}
		}
	}
	s.element = hff.NewUnionType(elements...)
	s.fallback = hff.NewUnionType(fallback...)
	s.fields = make(map[string]hff.Type, len(fields))
	for _, key := range s.keys {
		choices := append(append([]hff.Type(nil), fields[key]...), fallback...)
		s.fields[key] = hff.NewUnionType(choices...)
	}
	return s
}

// Validate the same per-position representation contract used by document
// normalization without constructing SQL or binding unused Struct key names.
func (b *builder) checkComparisonDocumentSchema(n hff.Node, format JSONFormat, typ hff.Type) error {
	if err := b.checkJSONStringSchemas(n, typ); err != nil {
		return err
	}
	if err := checkContext(b.ctx); err != nil {
		return err
	}
	shape := makeComparisonShape(typ)
	if shape.any {
		if format != JSON {
			return b.failure(n, "dynamic JSON equality requires preserved numeric types or type metadata")
		}
		return nil
	}
	if shape.integer && shape.floating && format != JSON {
		return b.failure(n, "dynamic JSON equality requires preserved numeric types or type metadata")
	}
	for _, member := range shape.members {
		switch member.Kind() {
		case hff.NullType, hff.NeverType, hff.ListType, hff.MapType, hff.StructType,
			hff.IntType, hff.FloatType, hff.BoolType, hff.StringType, hff.BytesType, hff.DurationType, hff.TimestampType:
		default:
			return b.failure(n, "dynamic JSON comparison cannot recover this schema type")
		}
	}
	if shape.list {
		if err := b.checkComparisonDocumentSchema(n, format, shape.element); err != nil {
			return err
		}
	}
	if shape.object {
		for _, key := range shape.keys {
			// Normalization binds Struct keys as text. Preserve its input
			// restrictions even when scalar dispatch needs no key parameters.
			if !utf8.ValidString(key) || strings.ContainsRune(key, '\x00') {
				return b.failure(n, "PostgreSQL text cannot encode invalid UTF-8 or NUL")
			}
			if err := b.checkComparisonDocumentSchema(n, format, shape.fields[key]); err != nil {
				return err
			}
		}
		return b.checkComparisonDocumentSchema(n, format, shape.fallback)
	}
	return nil
}
