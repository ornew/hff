package sql

import (
	"sort"
	"strings"

	"github.com/ornew/hff"
)

// A JSON category identifies a collection family, not an individual union
// member. Retain all possible element types before testing the actual values.
func (b *builder) jsonUnionCollectionMatches(n hff.Node, code string, format JSONFormat, source, target hff.Type) (string, error) {
	for _, member := range source.Members() {
		if member.Kind() == hff.AnyType {
			return b.dynamicJSONMatches(n, code, format, target)
		}
	}
	class := string(format) + "_typeof(" + code + ")"
	if target.Kind() == hff.ListType {
		var elements []hff.Type
		for _, member := range source.Members() {
			if member.Kind() == hff.ListType {
				elements = append(elements, member.Element())
			}
		}
		if len(elements) == 0 {
			return "FALSE", nil
		}
		test, err := b.jsonMatches(n, code, format, hff.NewListType(hff.NewUnionType(elements...)), target)
		if err != nil {
			return "", err
		}
		return both("("+class+"='array')", test), nil
	}
	// Each declared key retains its own candidate types. Map members contribute
	// their element type to every key, including keys absent from all structs.
	var maps []hff.Type
	fields := map[string][]hff.Type{}
	hasObject := false
	for _, member := range source.Members() {
		switch member.Kind() {
		case hff.MapType:
			hasObject = true
			maps = append(maps, member.Element())
		case hff.StructType:
			hasObject = true
			for _, field := range member.Fields() {
				fields[field.Name] = append(fields[field.Name], field.Type)
			}
		}
	}
	if !hasObject {
		return "FALSE", nil
	}
	alias := b.nextAlias()
	child := alias + ".value"
	fallback, err := b.jsonMatches(n, child, format, hff.NewUnionType(maps...), target.Element())
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var cases []string
	for _, name := range keys {
		candidates := append(append([]hff.Type{}, maps...), fields[name]...)
		test, err := b.jsonMatches(n, child, format, hff.NewUnionType(candidates...), target.Element())
		if err != nil {
			return "", err
		}
		key, err := b.parameter(n, hff.String(name))
		if err != nil {
			return "", err
		}
		cases = append(cases, "WHEN "+alias+".key="+key.code+" COLLATE \"C\" THEN "+test)
	}
	test := fallback
	if len(cases) > 0 {
		test = "(CASE " + strings.Join(cases, " ") + " ELSE " + fallback + " END)"
	}
	safe := "(CASE WHEN " + class + "='object' THEN " + code + " ELSE '{}'::" + string(format) + " END)"
	return both("("+class+"='object')", "NOT EXISTS (SELECT 1 FROM "+string(format)+"_each("+safe+") AS "+alias+" WHERE NOT COALESCE("+test+", FALSE))"), nil
}
