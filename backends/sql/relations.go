package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Relation describes a caller-owned PostgreSQL row source correlated to the
// enclosing query. Each selected source row represents one HFF list item.
// Construction and Build perform no database I/O; the caller owns the table,
// data/schema validity, database execution, and query timeouts.
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

// RelationOption configures a Relation. NewRelation applies options in order.
type RelationOption struct{ apply func(*relationConfig) error }

// WithRelationSource selects a literal table identifier and local SQL alias.
// The alias is visible to the correlation and item bindings.
func WithRelationSource(table ColumnRef, alias string) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.table = Column(table.parts...); c.alias = alias; return nil }}
}

// WithCorrelation supplies the predicate that relates source rows to the
// enclosing record. Build lowers it to a correlated PostgreSQL condition.
// Prefer typed SQLPredicate constructors; the condition must accurately and
// deterministically express the intended relationship.
func WithCorrelation(predicate SQLPredicate) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.correlation = predicate; return nil }}
}

// WithRelationState supplies explicit predicates for a relation's logical list
// presence and null state. Without it, the list is present and non-null even
// when the row source has no matching rows (an empty list). The caller owns the
// truth and deterministic behavior of these predicates.
func WithRelationState(present, null SQLPredicate) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.present = present; c.null = null; return nil }}
}

// WithRelationItem declares the exact HFF element type and maps each source row
// to one item. The Binding path must be empty because it is relative to that
// row; use BindStruct for a flattened struct item or another binding for its
// physical representation.
func WithRelationItem(typ hff.Type, item Binding) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.typ = typ; c.item = item; return nil }}
}

// RelationOrder is one component of an immutable typed row ordering. PostgreSQL
// scalar ordering is used. The caller guarantees the complete order tuple is
// unique whenever the HFF operation depends on stable element order.
type RelationOrder struct {
	column                 ColumnRef
	typ                    hff.Type
	descending, nullsFirst bool
}

// Ascending orders a relation by the given typed SQL column in ascending order.
func Ascending(column ColumnRef, typ hff.Type) RelationOrder {
	return RelationOrder{column: Column(column.parts...), typ: typ}
}

// Descending orders a relation by the given typed SQL column in descending order.
func Descending(column ColumnRef, typ hff.Type) RelationOrder {
	return RelationOrder{column: Column(column.parts...), typ: typ, descending: true}
}

// NullsFirst returns an ordering with null values before non-null values.
func NullsFirst(order RelationOrder) RelationOrder { order.nullsFirst = true; return order }

// WithUniqueOrder configures the order tuple that the caller guarantees is
// unique. Order is required only for operations whose results depend on item
// order, such as indexing, stable sort ties, order-observed map results, or folds.
func WithUniqueOrder(order ...RelationOrder) RelationOption {
	return RelationOption{func(c *relationConfig) error { c.order = append([]RelationOrder(nil), order...); return nil }}
}

// NewRelation validates and snapshots a caller-owned relation description.
// Source, correlation, and an item binding are required. State defaults to a
// present non-null list; order is optional unless a used operation depends on
// order. Identifiers are validated here, but table contents are not inspected.
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
	if c.correlation.node == nil && strings.TrimSpace(c.correlation.code) == "" {
		return Relation{}, configuration("relation requires a trusted correlation predicate")
	}
	for _, order := range c.order {
		if _, err := quoteColumn(order.column); err != nil {
			return Relation{}, err
		}
		kind, ok := scalarKind(order.typ)
		if !ok || kind == hff.NullType || kind == hff.BoolType || kind == hff.BytesType {
			return Relation{}, configuration("relation order requires a number, string, or duration column type")
		}
	}
	return Relation{config: c, valid: true}, nil
}

// BindRelation binds an HFF list path to a configured correlated row source.
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
	if _, err := b.checkJSONMetadata(c.item); err != nil {
		return err
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
			restored, _, restoreErr := b.jsonBindingCode(c.item)
			if restoreErr != nil {
				return "", restoreErr
			}
			item = "COALESCE(" + restored + "::json,'null'::json)"
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
	if len(orders) > 0 && !b.relationOrderBlind(n, binding) {
		ordered = " ORDER BY " + strings.Join(orders, ",")
	}
	rows := "(SELECT COALESCE(json_agg(" + item + ordered + "),'[]'::json) FROM " + table + " AS " + alias + " WHERE (" + b.predicateCode(c.correlation) + "))"
	return "(CASE WHEN (" + b.predicateCode(c.present) + ") IS NOT TRUE OR (" + b.predicateCode(c.null) + ") IS TRUE THEN NULL::json ELSE " + rows + " END)", nil
}

// Only consumers with an order-independent result can observe unordered lists.
// Guards preserve the consuming context; a changed or returned list requires
// an order unless its consumer can be proven order-independent separately.
func (b *builder) checkRelationOrder(n hff.Node, parent hff.Node, index int) error {
	return b.checkRelationConsumer(n, parent, index, nil, false)
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
func (b *builder) checkRelationConsumer(n hff.Node, parent hff.Node, index int, environment map[hff.BindingID]Binding, blind bool) error {
	if err := checkContext(b.ctx); err != nil {
		return err
	}
	binding, mapped := b.relationMapping(n, environment)
	if orderRelevantNode(n, binding, mapped) {
		provenBlind := blind || orderBlindConsumer(parent, index)
		if b.orderBlind == nil {
			b.orderBlind = make(map[hff.Node]bool)
		}
		if previous, seen := b.orderBlind[n]; seen {
			b.orderBlind[n] = previous && provenBlind
		} else {
			b.orderBlind[n] = provenBlind
		}
	}

	if mapped && !blind && observesRelationContents(parent, index) {
		observed := binding
		if method, ok := parent.Method(); ok && index == 0 && binding.relation != nil && (method.Name() == "contains" || method.Name() == "unique") {
			observed = binding.relation.config.item
		}
		if bindingHasUnorderedRelation(observed) {
			return b.failure(n, "relation contents require an explicitly unique order for this operation")
		}
	}
	if mapped && !blind && binding.relation != nil && len(binding.relation.config.order) == 0 {
		consumer := parent
		if consumer.Kind() == hff.GuardNode || consumer.Kind() == hff.TypeGuardNode {
		} else if consumer.Kind() == hff.BoolNode || consumer.Kind() == hff.NotNode || consumer.Kind() == hff.AndNode || consumer.Kind() == hff.OrNode || consumer.Kind() == hff.NullTestNode {
		} else if method, ok := consumer.Method(); ok && index == 0 {
			switch method.Name() {
			case "any", "all", "size", "has", "contains", "unique", "exists", "type":
			case "sort":
				if !canonicalScalarSort(consumer) {
					return b.failure(n, "stable sort requires relation ordering when equal values can retain distinct representations")
				}
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
		childBlind := false
		if n.Kind() == hff.GuardNode || n.Kind() == hff.TypeGuardNode {
			childBlind = blind
		}
		if method, ok := n.Method(); ok && (blind || orderBlindConsumer(parent, index)) {
			if (method.Name() == "map" || method.Name() == "sort") && i == 0 {
				childBlind = true
			}
			if method.Name() == "concat" && listNode(n) && concatOrderBlindSafe(b, n, environment) {
				childBlind = true
			}
		}
		if err := b.checkRelationConsumer(child, consumer, argument, nested, childBlind); err != nil {
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
	if c.item.format == JSONB && c.item.metadata == nil {
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
		case "sort":
			return !canonicalScalarSort(parent)
		}
	}
	return true
}

func listNode(n hff.Node) bool { kind, ok := jsonKind(n.Type()); return ok && kind == hff.ListType }

// Equal int or string values are indistinguishable even in stable-sort ties.
// Floats are excluded: signed zero and mixed numeric subtypes can be observed.
func canonicalScalarSort(n hff.Node) bool {
	method, ok := n.Method()
	if !ok || method.Name() != "sort" || !listNode(n) {
		return false
	}
	receiver, _ := n.Child(0)
	element := nonnullJSONType(receiver.Type()).Element()
	kind, ok := jsonKind(element)
	return ok && (kind == hff.IntType || kind == hff.StringType)
}
func orderBlindConsumer(parent hff.Node, index int) bool {
	switch parent.Kind() {
	case hff.BoolNode, hff.NotNode, hff.AndNode, hff.OrNode, hff.NullTestNode:
		return true
	}
	method, ok := parent.Method()
	if !ok || index != 0 {
		return false
	}
	switch method.Name() {
	case "any", "all", "size", "has", "contains", "unique", "exists", "type":
		return true
	case "sort":
		return canonicalScalarSort(parent)
	}
	return false
}
func concatOrderBlindSafe(b *builder, n hff.Node, environment map[hff.BindingID]Binding) bool {
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if mapping, ok := b.relationMapping(child, environment); ok && mapping.relation != nil && bindingHasUnorderedRelation(mapping.relation.config.item) {
			return false
		}
	}
	return true
}

// Outer sequence insensitivity says nothing about lists inside an item. When
// materializing a whole struct, keep each nested relation's own ordering.
func (b *builder) relationOrderBlind(n hff.Node, binding Binding) bool {
	if !b.orderBlind[n] {
		return false
	}
	observed, ok := b.relationMapping(n, b.physicalParameters)
	return ok && observed.relation == binding.relation
}

// Scalar and ordinary object nodes do not need sequence-order annotations.
// Avoid retaining them or traversing their types on normal query paths.
func orderRelevantNode(n hff.Node, mapping Binding, mapped bool) bool {
	if method, ok := n.Method(); ok {
		switch method.Name() {
		case "map", "concat", "sort":
			return true
		}
	}
	return mapped && mapping.relation != nil
}
