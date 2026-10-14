package config

import (
	"fmt"

	"github.com/ornew/hff"
)

type typeSpec struct {
	Kind    string      `json:"kind"`
	Element *typeSpec   `json:"element,omitempty"`
	Fields  []fieldSpec `json:"fields,omitempty"`
	Members []typeSpec  `json:"members,omitempty"`
}

type fieldSpec struct {
	Name     string   `json:"name"`
	Type     typeSpec `json:"type"`
	Optional bool     `json:"optional,omitempty"`
}

func encodeType(t hff.Type) (typeSpec, error) {
	if err := t.Check(); err != nil {
		return typeSpec{}, err
	}
	switch t.Kind() {
	case hff.NullType:
		return typeSpec{Kind: "null"}, nil
	case hff.BoolType:
		return typeSpec{Kind: "bool"}, nil
	case hff.IntType:
		return typeSpec{Kind: "int"}, nil
	case hff.FloatType:
		return typeSpec{Kind: "float"}, nil
	case hff.StringType:
		return typeSpec{Kind: "string"}, nil
	case hff.BytesType:
		return typeSpec{Kind: "bytes"}, nil
	case hff.TimestampType:
		return typeSpec{Kind: "timestamp"}, nil
	case hff.DurationType:
		return typeSpec{Kind: "duration"}, nil
	case hff.AnyType:
		return typeSpec{Kind: "any"}, nil
	case hff.NeverType:
		return typeSpec{Kind: "never"}, nil
	case hff.ListType, hff.MapType:
		element, err := encodeType(t.Element())
		if err != nil {
			return typeSpec{}, err
		}
		kind := "list"
		if t.Kind() == hff.MapType {
			kind = "map"
		}
		return typeSpec{Kind: kind, Element: &element}, nil
	case hff.StructType:
		out := typeSpec{Kind: "struct"}
		for _, f := range t.Fields() {
			ft, err := encodeType(f.Type)
			if err != nil {
				return typeSpec{}, err
			}
			out.Fields = append(out.Fields, fieldSpec{Name: f.Name, Type: ft, Optional: f.Optional})
		}
		return out, nil
	case hff.UnionType:
		out := typeSpec{Kind: "union"}
		for _, member := range t.Members() {
			mt, err := encodeType(member)
			if err != nil {
				return typeSpec{}, err
			}
			out.Members = append(out.Members, mt)
		}
		return out, nil
	default:
		return typeSpec{}, fmt.Errorf("unsupported schema kind")
	}
}

func decodeType(s typeSpec) (hff.Type, error) {
	if s.Kind == "" {
		return hff.Type{}, fmt.Errorf("schema type needs kind")
	}
	primitive := map[string]hff.TypeKind{
		"null": hff.NullType, "bool": hff.BoolType, "int": hff.IntType,
		"float": hff.FloatType, "string": hff.StringType, "bytes": hff.BytesType,
		"timestamp": hff.TimestampType, "duration": hff.DurationType,
		"any": hff.AnyType, "never": hff.NeverType,
	}
	var t hff.Type
	switch s.Kind {
	case "list", "map":
		if s.Element == nil || len(s.Fields) != 0 || len(s.Members) != 0 {
			return t, fmt.Errorf("%s type needs only element", s.Kind)
		}
		element, err := decodeType(*s.Element)
		if err != nil {
			return t, err
		}
		if s.Kind == "list" {
			t = hff.NewListType(element)
		} else {
			t = hff.NewMapType(element)
		}
	case "struct":
		if s.Element != nil || len(s.Members) != 0 {
			return t, fmt.Errorf("struct type cannot have element or members")
		}
		fields := make([]hff.Field, len(s.Fields))
		for i, f := range s.Fields {
			ft, err := decodeType(f.Type)
			if err != nil {
				return t, fmt.Errorf("field %q: %w", f.Name, err)
			}
			fields[i] = hff.Field{Name: f.Name, Type: ft, Optional: f.Optional}
		}
		t = hff.NewStructType(fields...)
	case "union":
		if s.Element != nil || len(s.Fields) != 0 || len(s.Members) < 2 {
			return t, fmt.Errorf("union type needs at least two members only")
		}
		members := make([]hff.Type, len(s.Members))
		for i, member := range s.Members {
			var err error
			members[i], err = decodeType(member)
			if err != nil {
				return t, err
			}
		}
		t = hff.NewUnionType(members...)
		if t.Kind() != hff.UnionType || len(t.Members()) != len(members) {
			return hff.Type{}, fmt.Errorf("union members must be distinct")
		}
	default:
		kind, ok := primitive[s.Kind]
		if !ok {
			return t, fmt.Errorf("unknown schema kind %q", s.Kind)
		}
		if s.Element != nil || len(s.Fields) != 0 || len(s.Members) != 0 {
			return t, fmt.Errorf("primitive type %q has composite fields", s.Kind)
		}
		t = hff.PrimitiveType(kind)
	}
	if err := t.Check(); err != nil {
		return hff.Type{}, err
	}
	return t, nil
}
