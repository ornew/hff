package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
)

func validateBindings(root hff.Type, bindings []sql.Binding) error {
	seen := map[string]bool{}
	physical := map[string]physicalColumn{}
	views := make([]sql.BindingView, 0, len(bindings))
	for _, binding := range bindings {
		view := binding.View()
		views = append(views, view)
		if len(view.Path) == 0 {
			return fmt.Errorf("top-level binding needs a schema path")
		}
		key, _ := json.Marshal(view.Path)
		if seen[string(key)] {
			return fmt.Errorf("duplicate binding path %s", key)
		}
		seen[string(key)] = true
		typ, err := bindingType(root, view.Path)
		if err != nil {
			return err
		}
		if err := validateBindingType(typ, view); err != nil {
			return fmt.Errorf("binding %s: %w", key, err)
		}
		if err := validatePhysicalBinding(typ, view, physical); err != nil {
			return fmt.Errorf("binding %s: %w", key, err)
		}
	}
	for _, parent := range views {
		if parent.Kind != "struct" && parent.Kind != "relation" && parent.Kind != "json" && parent.Kind != "composite" {
			continue
		}
		for _, child := range views {
			if len(child.Path) > len(parent.Path) && slices.Equal(child.Path[:len(parent.Path)], parent.Path) {
				return fmt.Errorf("structured binding overlaps a descendant binding")
			}
		}
	}
	return nil
}

type physicalColumn struct {
	format  string
	kind    hff.TypeKind
	typeKey string
}

func validatePhysicalBinding(typ hff.Type, view sql.BindingView, columns map[string]physicalColumn) error {
	declared := typ
	typ = nonNullType(typ)
	switch view.Kind {
	case "struct":
		fields := map[string]hff.Type{}
		for _, field := range typ.Fields() {
			fields[field.Name] = field.Type
		}
		for _, child := range view.Fields {
			if err := validatePhysicalBinding(fields[child.Path[0]], child, columns); err != nil {
				return err
			}
		}
		return nil
	case "relation":
		// Correlated row sources have their own column/alias scope.
		return validatePhysicalBinding(view.Relation.ItemType, view.Relation.Item, map[string]physicalColumn{})
	}
	column, _ := json.Marshal(view.Column)
	key := string(column)
	signature := physicalColumn{}
	switch view.Kind {
	case "native":
		signature.kind = typ.Kind()
	case "timestamp":
		signature.format, signature.kind = "timestamp", hff.TimestampType
	case "json":
		signature.format, signature.typeKey = string(view.Format), declared.Key()
	case "composite":
		signature.format, signature.typeKey = "composite", declared.Key()
	}
	if previous, found := columns[key]; found && previous != signature {
		return fmt.Errorf("SQL column has conflicting physical formats or schema types")
	}
	columns[key] = signature
	if view.Kind == "timestamp" && len(view.TimestampRemainder) != 0 {
		remainder, _ := json.Marshal(view.TimestampRemainder)
		remainderKey := string(remainder)
		if _, found := columns[remainderKey]; found || remainderKey == key {
			return fmt.Errorf("Timestamp remainder column conflicts with another binding")
		}
		columns[remainderKey] = physicalColumn{format: "timestamp.remainder"}
	}
	return nil
}

func bindingType(root hff.Type, path []string) (hff.Type, error) {
	current := root
	for i, name := range path {
		if current.Kind() != hff.StructType {
			return hff.Type{}, fmt.Errorf("binding traverses a non-struct schema at %q", name)
		}
		var field hff.Field
		found := false
		for _, candidate := range current.Fields() {
			if candidate.Name == name {
				field, found = candidate, true
				break
			}
		}
		if !found {
			return hff.Type{}, fmt.Errorf("binding names undeclared field %q", name)
		}
		if field.Optional && i < len(path)-1 {
			return hff.Type{}, fmt.Errorf("binding traverses optional parent %q", name)
		}
		current = field.Type
	}
	return current, nil
}

func nonNullType(t hff.Type) hff.Type {
	if t.Kind() != hff.UnionType {
		return t
	}
	members := t.Members()
	if len(members) != 2 {
		return t
	}
	if members[0].Kind() == hff.NullType {
		return members[1]
	}
	if members[1].Kind() == hff.NullType {
		return members[0]
	}
	return t
}

func validateBindingType(t hff.Type, view sql.BindingView) error {
	t = nonNullType(t)
	switch view.Kind {
	case "native":
		if _, ok := predicateScalarKind(t); !ok && t.Kind() != hff.BytesType {
			return fmt.Errorf("native column requires a supported scalar type")
		}
	case "timestamp":
		if t.Kind() != hff.TimestampType {
			return fmt.Errorf("timestamp binding requires Timestamp schema")
		}
	case "composite":
		if t.Kind() != hff.StructType {
			return fmt.Errorf("composite binding requires struct schema")
		}
	case "json":
		if view.Format != sql.JSON && view.Format != sql.JSONB {
			return fmt.Errorf("unsupported JSON format")
		}
	case "struct":
		if t.Kind() != hff.StructType {
			return fmt.Errorf("flattened binding requires struct schema")
		}
		fields := map[string]hff.Field{}
		for _, field := range t.Fields() {
			fields[field.Name] = field
		}
		seen := map[string]bool{}
		for _, child := range view.Fields {
			if len(child.Path) != 1 || seen[child.Path[0]] {
				return fmt.Errorf("struct child paths must be distinct field names")
			}
			seen[child.Path[0]] = true
			field, ok := fields[child.Path[0]]
			if !ok {
				return fmt.Errorf("undeclared struct child %q", child.Path[0])
			}
			if err := validateBindingType(field.Type, child); err != nil {
				return fmt.Errorf("child %q: %w", child.Path[0], err)
			}
		}
		for _, field := range t.Fields() {
			if !field.Optional && !seen[field.Name] {
				return fmt.Errorf("required struct child %q is unbound", field.Name)
			}
		}
	case "relation":
		if t.Kind() != hff.ListType || view.Relation == nil {
			return fmt.Errorf("relation binding requires list schema")
		}
		r := view.Relation
		if !t.Element().Equal(r.ItemType) {
			return fmt.Errorf("relation item type differs from list element")
		}
		if len(r.Item.Path) != 0 {
			return fmt.Errorf("relation item path must be empty")
		}
		if err := validateBindingType(r.ItemType, r.Item); err != nil {
			return fmt.Errorf("relation item: %w", err)
		}
	default:
		return fmt.Errorf("unsupported binding kind %q", view.Kind)
	}
	return nil
}

func validateTrustedPredicate(view sql.PredicateView) error {
	if view.Kind != "trusted" || strings.TrimSpace(view.TrustedCode) == "" || !utf8.ValidString(view.TrustedCode) || containsNUL(view.TrustedCode) {
		return fmt.Errorf("trusted predicate registry entry must contain fixed valid SQL")
	}
	for _, reference := range view.References {
		if err := validateColumn(reference); err != nil {
			return err
		}
	}
	return nil
}
