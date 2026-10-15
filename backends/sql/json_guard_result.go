package sql

import (
	"sort"

	"github.com/ornew/hff"
)

// Guard normalization reads only constrained positions. Any keeps its physical
// representation and does not establish a whole-document normalization proof.
func (b *builder) jsonGuardResult(n hff.Node, raw string, format JSONFormat, source, target hff.Type) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if !guardTargetRecoversNumbers(target) {
		return raw, nil
	}
	targets := []hff.Type{target}
	if target.Kind() == hff.UnionType {
		targets = target.Members()
	}
	for _, t := range targets {
		if t.Kind() == hff.AnyType {
			return raw, nil
		}
	}
	groups := map[string][]hff.Type{}
	var categories []string
	for _, t := range targets {
		category := jsonCategory(t.Kind())
		if category == "" {
			continue
		}
		if _, ok := groups[category]; !ok {
			categories = append(categories, category)
		}
		groups[category] = append(groups[category], t)
	}
	result := "CASE WHEN " + raw + " IS NULL OR " + string(format) + "_typeof(" + raw + ")='null' THEN NULL::" + string(format)
	for _, category := range categories {
		if err := checkContext(b.ctx); err != nil {
			return "", err
		}
		members := groups[category]
		branch := raw
		if len(categories) > 1 {
			branch = "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='" + category + "' THEN " + raw + " ELSE NULL::" + string(format) + " END)"
		}
		var value string
		var err error
		if category == "array" || category == "object" {
			var elements []hff.Type
			for _, member := range members {
				elements = append(elements, member.Element())
			}
			element := guardTargetUnion(elements)
			if element.Kind() == hff.AnyType {
				value = branch
			} else {
				value, err = b.jsonGuardContainer(n, branch, format, source, element, category)
			}
		} else {
			var observed hff.Type
			observed, err = b.guardSourceCategory(source, category)
			if err != nil {
				return "", err
			}
			typ := intersectItemType(observed, hff.NewUnionType(members...))
			if typ.Kind() == hff.NeverType {
				value = branch
			} else {
				value, err = b.normalizedComparisonDocument(n, branch, format, typ)
				if err == nil && format == JSONB {
					value = "(" + value + ")::jsonb"
				}
			}
		}
		if err != nil {
			return "", err
		}
		result += " WHEN " + string(format) + "_typeof(" + raw + ")='" + category + "' THEN " + value
	}
	return "(" + result + " ELSE " + raw + " END)", nil
}

func guardTargetUnion(types []hff.Type) hff.Type {
	for _, typ := range types {
		if typ.Kind() == hff.AnyType {
			return typ
		}
		if typ.Kind() == hff.UnionType {
			for _, member := range typ.Members() {
				if member.Kind() == hff.AnyType {
					return member
				}
			}
		}
	}
	return hff.NewUnionType(types...)
}

func (b *builder) guardSourceCategory(source hff.Type, category string) (hff.Type, error) {
	members := []hff.Type{source}
	if source.Kind() == hff.UnionType {
		members = source.Members()
	}
	var matching []hff.Type
	for _, member := range members {
		if err := checkContext(b.ctx); err != nil {
			return hff.Type{}, err
		}
		if member.Kind() == hff.AnyType || jsonCategory(member.Kind()) == category {
			matching = append(matching, member)
		}
	}
	return hff.NewUnionType(matching...), nil
}

func (b *builder) jsonGuardContainer(n hff.Node, raw string, format JSONFormat, source, element hff.Type, category string) (string, error) {
	var err error
	source, err = b.guardSourceCategory(source, category)
	if err != nil {
		return "", err
	}
	members := []hff.Type{source}
	if source.Kind() == hff.UnionType {
		members = source.Members()
	}
	var fallback []hff.Type
	fields := map[string][]hff.Type{}
	var keys []string
	for _, member := range members {
		if err := checkContext(b.ctx); err != nil {
			return "", err
		}
		switch member.Kind() {
		case hff.AnyType:
			fallback = append(fallback, member)
		case hff.ListType, hff.MapType:
			fallback = append(fallback, member.Element())
		case hff.StructType:
			for _, field := range member.Fields() {
				if err := checkContext(b.ctx); err != nil {
					return "", err
				}
				if _, ok := fields[field.Name]; !ok {
					keys = append(keys, field.Name)
				}
				fields[field.Name] = append(fields[field.Name], field.Type)
			}
		}
	}
	sort.Strings(keys)
	alias := b.nextAlias()
	child := alias + ".value"
	value, err := b.jsonGuardResult(n, child, format, hff.NewUnionType(fallback...), element)
	if err != nil {
		return "", err
	}
	if category == "array" {
		safe := "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='array' THEN " + raw + " ELSE '[]'::" + string(format) + " END)"
		return "(SELECT COALESCE(" + string(format) + "_agg(" + value + " ORDER BY " + alias + ".ord),'[]'::" + string(format) + ") FROM " + string(format) + "_array_elements(" + safe + ") WITH ORDINALITY AS " + alias + "(value,ord))", nil
	}
	if len(keys) > 0 {
		cases := "CASE"
		for _, key := range keys {
			typ := hff.NewUnionType(append(append([]hff.Type(nil), fields[key]...), fallback...)...)
			branch, err := b.jsonGuardResult(n, child, format, typ, element)
			if err != nil {
				return "", err
			}
			parameter, err := b.parameter(n, hff.String(key))
			if err != nil {
				return "", err
			}
			cases += " WHEN " + alias + ".key COLLATE \"C\"=" + parameter.code + " COLLATE \"C\" THEN " + branch
		}
		value = "(" + cases + " ELSE " + value + " END)"
	}
	safe := "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='object' THEN " + raw + " ELSE '{}'::" + string(format) + " END)"
	return "(SELECT COALESCE(" + string(format) + "_object_agg(" + alias + ".key," + value + "),'{}'::" + string(format) + ") FROM " + string(format) + "_each(" + safe + ") AS " + alias + "(key,value))", nil
}

func guardTargetRecoversNumbers(target hff.Type) bool {
	switch target.Kind() {
	case hff.AnyType:
		return false
	case hff.IntType, hff.FloatType:
		return true
	case hff.ListType, hff.MapType:
		return guardTargetRecoversNumbers(target.Element())
	case hff.UnionType:
		if guardTargetUnion(target.Members()).Kind() == hff.AnyType {
			return false
		}
		for _, member := range target.Members() {
			if guardTargetRecoversNumbers(member) {
				return true
			}
		}
	}
	return false
}
