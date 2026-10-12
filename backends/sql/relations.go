package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Relation describes a caller-owned correlated row source. Construction and
// translation perform no database I/O. Every source row is one list item.
type Relation struct {
	config relationConfig
	valid  bool
}
type relationConfig struct {
	table         ColumnRef
	alias         string
	correlation   SQLPredicate
	present, null SQLPredicate
	typ           hff.Type
	item          Binding
	order         []RelationOrder
}
type RelationOption struct{ apply func(*relationConfig) error }

func WithRelationSource(table ColumnRef, alias string) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.table = Column(table.parts...); c.alias = alias; return nil }}
}
func WithCorrelation(predicate SQLPredicate) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.correlation = predicate; return nil }}
}

// WithRelationState distinguishes a missing or null list from an empty row
// source. Without it, a relation is present and non-null, including no rows.
func WithRelationState(present, null SQLPredicate) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.present = present; c.null = null; return nil }}
}

// The item binding uses an empty path, relative to each related row.
func WithRelationItem(typ hff.Type, item Binding) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.typ = typ; c.item = item; return nil }}
}

// RelationOrder is an immutable typed column ordering. PostgreSQL's native
// scalar ordering is used; the caller guarantees the full tuple is unique.
type RelationOrder struct {
	column                 ColumnRef
	typ                    hff.Type
	descending, nullsFirst bool
}

func Ascending(column ColumnRef, typ hff.Type) RelationOrder {
	return RelationOrder{column: Column(column.parts...), typ: typ}
}
func Descending(column ColumnRef, typ hff.Type) RelationOrder {
	return RelationOrder{column: Column(column.parts...), typ: typ, descending: true}
}
func NullsFirst(order RelationOrder) RelationOrder { order.nullsFirst = true; return order }
func WithUniqueOrder(order ...RelationOrder) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.order = append([]RelationOrder(nil), order...); return nil }}
}
func NewRelation(options ...RelationOption) (Relation, error) {
	c := relationConfig{present: TrustedSQL("true"), null: TrustedSQL("false")}
	for _, option := range options {
		if option.apply == nil {
			return Relation{}, configuration("invalid RelationOption")
		}
		if err := option.apply(&c); err != nil {
			return Relation{}, err
		}
	}
	if _, err := quoteColumn(c.table); err != nil {
		return Relation{}, err
	}
	if _, err := quoteColumn(Column(c.alias)); err != nil {
		return Relation{}, err
	}
	if c.typ.Kind() == hff.InvalidType || len(c.item.path) != 0 {
		return Relation{}, configuration("relation requires an explicit item type and an empty relative item path")
	}
	if strings.TrimSpace(c.correlation.code) == "" {
		return Relation{}, configuration("relation requires a trusted correlation predicate")
	}
	for _, order := range c.order {
		if _, err := quoteColumn(order.column); err != nil {
			return Relation{}, err
		}
		kind, ok := scalarKind(order.typ)
		if !ok || kind == hff.NullType || kind == hff.BoolType {
			return Relation{}, configuration("relation order requires a number or string column type")
		}
	}
	return Relation{config: c, valid: true}, nil
}
func BindRelation(path hff.Path, relation Relation) Binding {
	return Binding{path: append(hff.Path(nil), path...), relation: &relation}
}

func (b *builder) checkRelation(binding Binding, typ hff.Type, kinds map[string]hff.TypeKind, formats map[string]JSONFormat, types map[string]string) error {
	relation := binding.relation
	if relation == nil || !relation.valid {
		return configuration("invalid Relation")
	}
	kind, ok := jsonKind(typ)
	if !ok || kind != hff.ListType || nonnullJSONType(typ).Element().Key() != relation.config.typ.Key() {
		return configuration("relation item type must exactly match the bound list element type")
	}
	c := relation.config
	// Column aliases belong to this correlated subquery, not the enclosing
	// binding scope. Two independent relations can reuse the same alias for
	// different physical tables and element types.
	kinds = make(map[string]hff.TypeKind)
	formats = make(map[string]JSONFormat)
	types = make(map[string]string)
	for _, predicate := range []SQLPredicate{c.correlation, c.present, c.null} {
		if err := b.checkPredicate(predicate); err != nil {
			return err
		}
	}
	b.reserved[c.alias] = true
	for _, part := range c.table.parts {
		b.reserved[part] = true
	}
	for _, order := range c.order {
		for _, part := range order.column.parts {
			b.reserved[part] = true
		}
	}
	if c.item.structure != nil {
		return b.checkStructBinding(c.item, c.typ, kinds, formats, types)
	}
	if c.item.relation != nil {
		return b.checkRelation(c.item, c.typ, kinds, formats, types)
	}
	if _, err := quoteColumn(c.item.column); err != nil {
		return err
	}
	if c.item.format != "" && c.item.format != JSON && c.item.format != JSONB {
		return configuration("invalid relation item JSON format")
	}
	for _, part := range c.item.column.parts {
		b.reserved[part] = true
	}
	return nil
}
func (b *builder) relationJSON(n hff.Node, binding Binding) (string, error) {
	c := binding.relation.config
	table, _ := quoteColumn(c.table)
	alias, _ := quoteColumn(Column(c.alias))
	var item string
	var err error
	if c.item.structure != nil {
		item, err = b.structJSON(n, c.item, c.typ)
	} else if c.item.relation != nil {
		item, err = b.relationJSON(n, c.item)
	} else {
		column, _ := quoteColumn(c.item.column)
		if c.item.format != "" {
			item = "COALESCE(" + column + "::json,'null'::json)"
		} else {
			item, err = b.compositeJSON(n, column, c.typ)
		}
	}
	if err != nil {
		return "", err
	}
	var orders []string
	for _, order := range c.order {
		column, _ := quoteColumn(order.column)
		direction, nulls := " ASC", " NULLS LAST"
		if order.descending {
			direction = " DESC"
		}
		if order.nullsFirst {
			nulls = " NULLS FIRST"
		}
		orders = append(orders, column+direction+nulls)
	}
	ordered := ""
	if len(orders) > 0 {
		ordered = " ORDER BY " + strings.Join(orders, ",")
	}
	rows := "(SELECT COALESCE(json_agg(" + item + ordered + "),'[]'::json) FROM " + table + " AS " + alias + " WHERE (" + c.correlation.code + ") IS TRUE)"
	return "(CASE WHEN (" + c.present.code + ") IS NOT TRUE OR (" + c.null.code + ") IS TRUE THEN NULL::json ELSE " + rows + " END)", nil
}

// Only consumers with an order-independent result can observe unordered lists.
// Guards preserve the consuming context; a changed or returned list requires
// an order unless its consumer can be proven order-independent separately.
func (b *builder) checkRelationOrder(n hff.Node, parent hff.Node, index int) error {
	return b.checkRelationConsumer(n, parent, index, nil)
}
func (b *builder) relationMapping(n hff.Node, environment map[hff.BindingID]Binding) (Binding, bool) {
	if n.Kind() == hff.ParameterNode {
		parameter, ok := n.Binding()
		if !ok {
			return Binding{}, false
		}
		mapping, ok := environment[parameter.ID()]
		return mapping, ok
	}
	if path, ok := n.Path(); ok {
		if mapping, ok := b.relationBindings[pathKey(path)]; ok {
			return mapping, true
		}
		if mapping, ok := b.structBindings[pathKey(path)]; ok {
			return mapping, true
		}
	}
	switch n.Kind() {
	case hff.GuardNode, hff.TypeGuardNode:
		child, _ := n.Child(0)
		return b.relationMapping(child, environment)
	case hff.FieldNode:
		parent, _ := n.Child(0)
		mapping, ok := b.relationMapping(parent, environment)
		if !ok || mapping.structure == nil {
			return Binding{}, false
		}
		name, _ := n.Field()
		for _, field := range mapping.structure.fields {
			if field.path[0] == name {
				return field, true
			}
		}
	case hff.IndexNode:
		receiver, _ := n.Child(0)
		mapping, ok := b.relationMapping(receiver, environment)
		if ok && mapping.relation != nil {
			return mapping.relation.config.item, true
		}
	}
	return Binding{}, false
}
func (b *builder) checkRelationConsumer(n hff.Node, parent hff.Node, index int, environment map[hff.BindingID]Binding) error {
	if err := checkContext(b.ctx); err != nil {
		return err
	}

	if binding, exists := b.relationMapping(n, environment); exists && observesRelationContents(parent, index) {
		observed := binding
		if method, ok := parent.Method(); ok && index == 0 && binding.relation != nil && (method.Name() == "contains" || method.Name() == "unique") {
			observed = binding.relation.config.item
		}
		if bindingHasUnorderedRelation(observed) {
			return b.failure(n, "relation contents require an explicitly unique order for this operation")
		}
	}
	if binding, exists := b.relationMapping(n, environment); exists && binding.relation != nil && len(binding.relation.config.order) == 0 {
		consumer := parent
		if consumer.Kind() == hff.GuardNode || consumer.Kind() == hff.TypeGuardNode {
		} else if consumer.Kind() == hff.BoolNode || consumer.Kind() == hff.NotNode || consumer.Kind() == hff.AndNode || consumer.Kind() == hff.OrNode || consumer.Kind() == hff.NullTestNode {
		} else if method, ok := consumer.Method(); ok && index == 0 {
			switch method.Name() {
			case "any", "all", "size", "has", "contains", "unique", "exists", "type":
			default:
				return b.failure(n, "relation requires an explicitly unique order for this operation")
			}
		} else {
			return b.failure(n, "relation requires an explicitly unique order for this operation")
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		consumer, argument := n, i
		if n.Kind() == hff.GuardNode || n.Kind() == hff.TypeGuardNode {
			consumer, argument = parent, index
		}
		nested := environment
		if parameters, ok := child.Parameters(); ok {
			receiver, _ := n.Child(0)
			if mapping, found := b.relationMapping(receiver, environment); found && mapping.relation != nil {
				nested = make(map[hff.BindingID]Binding, len(environment)+len(parameters))
				for id, value := range environment {
					nested[id] = value
				}
				if len(parameters) == 1 {
					nested[parameters[0].ID()] = mapping.relation.config.item
				}
				if len(parameters) == 2 {
					// The element is the last parameter of foldl and the first of foldr.
					element := 1
					if method, ok := n.Method(); ok && method.Name() == "foldr" {
						element = 0
					}
					nested[parameters[element].ID()] = mapping.relation.config.item
				}
			}
		}
		if err := b.checkRelationConsumer(child, consumer, argument, nested); err != nil {
			return err
		}
	}
	return nil
}

func relationLosesFloat(relation Relation) bool {
	c := relation.config
	if !jsonHasFloat(c.typ) {
		return false
	}
	if c.item.format == JSONB {
		return true
	}
	if c.item.structure != nil {
		return structLosesFloat(c.item, c.typ, nil)
	}
	if c.item.relation != nil {
		return relationLosesFloat(*c.item.relation)
	}
	return false
}

func bindingHasUnorderedRelation(binding Binding) bool {
	if binding.relation != nil {
		c := binding.relation.config
		return len(c.order) == 0 || bindingHasUnorderedRelation(c.item)
	}
	if binding.structure != nil {
		for _, field := range binding.structure.fields {
			if bindingHasUnorderedRelation(field) {
				return true
			}
		}
	}
	return false
}
func observesRelationContents(parent hff.Node, index int) bool {
	switch parent.Kind() {
	case hff.BoolNode, hff.NotNode, hff.AndNode, hff.OrNode, hff.NullTestNode, hff.GuardNode, hff.TypeGuardNode, hff.FieldNode, hff.IndexNode:
		return false
	}
	if method, ok := parent.Method(); ok && index == 0 {
		switch method.Name() {
		case "any", "all", "size", "has", "exists", "type", "map", "flatmap", "foldl", "foldr", "keys":
			return false
		}
	}
	return true
}
