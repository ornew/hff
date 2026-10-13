package sql

import (
	"github.com/ornew/hff"
)

// JSON's string class does not identify an HFF String by itself. A declared
// union may instead use that class for Bytes or Duration. Resolve the class
// through the stored schema before exposing type() to a query.
func (b *builder) jsonRuntimeTypeName(n hff.Node, code string, format JSONFormat, stored hff.Type) (string, error) {
	if err := b.checkJSONStringSchemas(n, stored); err != nil {
		return "", err
	}
	stringName := "string"
	members := []hff.Type{stored}
	if stored.Kind() == hff.UnionType {
		members = stored.Members()
	}
	for _, member := range members {
		switch member.Kind() {
		case hff.BytesType:
			stringName = "bytes"
		case hff.DurationType:
			stringName = "duration"
		}
	}
	class := string(format) + "_typeof(" + code + ")"
	return "(CASE " + class + " WHEN 'array' THEN 'list' WHEN 'boolean' THEN 'bool' WHEN 'string' THEN '" + stringName + "' ELSE COALESCE(" + class + ",'null') END)", nil
}

// A finite union with one string-family member can distinguish that member
// by its JSON class. Any remains unconstrained and needs its separate dynamic
// guard rules; a union with two string-family members is rejected by the
// schema check before this helper is called.
func jsonUnionStringMatch(code string, format JSONFormat, source, target hff.Type) (string, bool) {
	if source.Kind() != hff.UnionType {
		return "", false
	}
	switch target.Kind() {
	case hff.StringType, hff.BytesType, hff.DurationType:
	default:
		return "", false
	}
	stringKind := hff.InvalidType
	for _, member := range source.Members() {
		if jsonCategory(member.Kind()) == "string" {
			stringKind = member.Kind()
			break
		}
	}
	if stringKind != target.Kind() {
		return "FALSE", true
	}
	return "(" + string(format) + "_typeof(" + code + ")='string')", true
}
