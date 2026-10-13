package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Use the correlated source directly when its receiver is a plain bound path.
// Guards and computed lists retain the general no-value-aware lowering.
func (b *builder) relationPredicate(n hff.Node, name string) (expression, bool, error) {
	receiver, _ := n.Child(0)
	root := receiver
	for root.Kind() == hff.FieldNode {
		root, _ = root.Child(0)
	}
	if root.Kind() != hff.VariableNode && root.Kind() != hff.ParameterNode {
		return expression{}, false, nil
	}
	mapping, ok := b.relationMapping(receiver, b.physicalParameters)
	if !ok || mapping.relation == nil {
		return expression{}, false, nil
	}
	c := mapping.relation.config
	// Caller aliases are scoped SQL identifiers. Keep the general JSON path
	// when a nested source would shadow an active row or an outer binding.
	if b.activeRelationAliases[c.alias] > 0 || b.shadowsOuterBinding(c.alias) {
		return expression{}, false, nil
	}
	if b.activeRelationAliases == nil {
		b.activeRelationAliases = make(map[string]int)
	}
	b.activeRelationAliases[c.alias]++
	defer func() { b.activeRelationAliases[c.alias]-- }()

	callback, _ := n.Child(1)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 1 {
		return expression{}, true, b.failure(n, "collection predicate requires one callback parameter")
	}
	// Field selection through flattened ancestors must retain their states.
	absent := []string{"(" + b.predicateCode(c.present) + ") IS NOT TRUE", "(" + b.predicateCode(c.null) + ") IS TRUE"}
	parent := receiver
	for parent.Kind() == hff.FieldNode {
		parent, _ = parent.Child(0)
		if ancestor, ok := b.relationMapping(parent, b.physicalParameters); ok && ancestor.structure != nil {
			absent = append(absent, "("+b.predicateCode(ancestor.structure.present)+") IS NOT TRUE", "("+b.predicateCode(ancestor.structure.null)+") IS TRUE")
		}
	}
	outerSteps, outerPaths := b.steps, b.sharedPaths
	b.steps = nil
	b.sharedPaths = nil
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	if b.physicalParameters == nil {
		b.physicalParameters = make(map[hff.BindingID]Binding)
	}
	if b.parameterItems == nil {
		b.parameterItems = make(map[hff.BindingID]Binding)
	}
	id := parameters[0].ID()
	previous, existed := b.parameters[id]
	previousPhysical, physicalExisted := b.physicalParameters[id]
	b.physicalParameters[id] = c.item
	defer func() {
		b.steps = outerSteps
		b.sharedPaths = outerPaths
		if existed {
			b.parameters[id] = previous
		} else {
			delete(b.parameters, id)
		}
		if physicalExisted {
			b.physicalParameters[id] = previousPhysical
		} else {
			delete(b.physicalParameters, id)
		}
	}()
	// A row value is only materialized if the callback observes the item itself.
	// Field chains can instead project directly from its physical binding.
	b.parameters[id] = jsonParameter{typ: c.typ, format: JSON, lossy: relationLosesFloat(*mapping.relation)}
	previousItem, itemExisted := b.parameterItems[id]
	b.parameterItems[id] = c.item
	defer func() {
		if itemExisted {
			b.parameterItems[id] = previousItem
		} else {
			delete(b.parameterItems, id)
		}
	}()
	body, _ := callback.Child(0)
	result, err := b.expression(body)
	if err != nil {
		return expression{}, true, err
	}
	predicate := truth(result)
	operator := "EXISTS"
	if name == "all" {
		operator = "NOT EXISTS"
		predicate = "(NOT " + predicate + ")"
	}
	table, _ := quoteColumn(c.table)
	alias, _ := quoteColumn(Column(c.alias))
	from := table + " AS " + alias
	if len(b.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	}
	code := "(CASE WHEN " + strings.Join(absent, " OR ") + " THEN NULL ELSE " + operator + " (SELECT 1 FROM " + from + " WHERE (" + b.predicateCode(c.correlation) + ") AND " + predicate + ") END)"
	return expression{code: code, present: "TRUE", kind: hff.BoolType}, true, nil
}

// Relation cardinality depends on the correlated row set, not on the physical
// representation of each item. Count rows directly so a native item does not
// have to be materialized as JSON merely to answer size().
func (b *builder) relationSize(n hff.Node) (expression, bool, error) {
	receiver, _ := n.Child(0)
	root := receiver
	for root.Kind() == hff.FieldNode {
		root, _ = root.Child(0)
	}
	if root.Kind() != hff.VariableNode && root.Kind() != hff.ParameterNode {
		return expression{}, false, nil
	}
	mapping, ok := b.relationMapping(receiver, b.physicalParameters)
	if !ok || mapping.relation == nil {
		return expression{}, false, nil
	}
	c := mapping.relation.config
	if b.activeRelationAliases[c.alias] > 0 || b.shadowsOuterBinding(c.alias) {
		return expression{}, false, nil
	}
	absent := []string{"(" + b.predicateCode(c.present) + ") IS NOT TRUE", "(" + b.predicateCode(c.null) + ") IS TRUE"}
	parent := receiver
	for parent.Kind() == hff.FieldNode {
		parent, _ = parent.Child(0)
		if ancestor, ok := b.relationMapping(parent, b.physicalParameters); ok && ancestor.structure != nil {
			absent = append(absent, "("+b.predicateCode(ancestor.structure.present)+") IS NOT TRUE", "("+b.predicateCode(ancestor.structure.null)+") IS TRUE")
		}
	}
	table, err := quoteColumn(c.table)
	if err != nil {
		return expression{}, true, err
	}
	alias, err := quoteColumn(Column(c.alias))
	if err != nil {
		return expression{}, true, err
	}
	code := "(CASE WHEN " + strings.Join(absent, " OR ") + " THEN NULL::bigint ELSE (SELECT count(*) FROM " + table + " AS " + alias + " WHERE (" + b.predicateCode(c.correlation) + ")) END)"
	return expression{code: code, present: "TRUE", kind: hff.IntType}, true, nil
}

// A relation's valid list indices depend only on its cardinality. This also
// handles negative indices without requiring a stable order or item encoding.
func (b *builder) relationHas(n hff.Node) (expression, bool, error) {
	argumentNode, _ := n.Child(1)
	// Dynamic arguments retain JSON-aware family dispatch and runtime checks.
	staticKind, scalar := scalarKind(argumentNode.Type())
	if argumentNode.Type().Kind() == hff.NeverType {
		staticKind, scalar = hff.NeverType, true
	}
	if !scalar || (staticKind != hff.IntType && staticKind != hff.FloatType && staticKind != hff.NullType && staticKind != hff.NeverType) {
		return expression{}, false, nil
	}
	size, handled, err := b.relationSize(n)
	if !handled || err != nil {
		return expression{}, handled, err
	}
	size.code = b.project(size.code)
	argument, err := b.expression(argumentNode)
	if err != nil {
		return expression{}, true, err
	}
	switch argument.kind {
	case hff.IntType, hff.FloatType, hff.NullType, hff.NeverType:
	default:
		return expression{}, true, b.failure(argumentNode, "relation has requires a recoverable numeric index")
	}
	present := ordinaryPresence([]expression{size, argument})
	if argument.kind == hff.NullType || argument.kind == hff.NeverType {
		return expression{code: "NULL::boolean", present: present, kind: hff.BoolType}, true, nil
	}
	integral := "TRUE"
	index := argument.code
	if argument.kind == hff.FloatType {
		integral = both("("+argument.code+" = trunc("+argument.code+"))", validFloatIndex(argument.code))
		// PostgreSQL promotes bigint COUNT to float8 and can round a list
		// boundary above 2^53. Convert only after the range check.
		index = "(" + argument.code + ")::bigint"
	}
	code := "(CASE WHEN " + size.code + " IS NULL OR " + argument.code + " IS NULL OR NOT " + integral + " THEN NULL::boolean ELSE (" + index + " >= -" + size.code + " AND " + index + " < " + size.code + ") END)"
	return expression{code: code, present: present, kind: hff.BoolType}, true, nil
}

func (b *builder) physicalItemJSON(n hff.Node, mapping Binding, typ hff.Type) (string, JSONFormat, error) {
	switch {
	case mapping.structure != nil:
		code, err := b.structJSON(n, mapping, typ)
		return code, JSON, err
	case mapping.relation != nil:
		code, err := b.relationJSON(n, mapping)
		return code, JSON, err
	case mapping.timestamp:
		return "", JSON, b.failure(n, "whole Timestamp relation items require native predicate lowering")
	case mapping.format != "":
		return b.jsonBindingCode(mapping)
	default:
		column, err := quoteColumn(mapping.column)
		if err != nil {
			return "", JSON, err
		}
		code, err := b.compositeJSON(n, column, typ)
		return code, JSON, err
	}
}

func (b *builder) shadowsOuterBinding(alias string) bool {
	prefix, _ := quoteColumn(Column(alias))
	prefix += "."
	for _, column := range b.bindings {
		if strings.HasPrefix(column, prefix) {
			return true
		}
	}
	for _, mapping := range b.physicalParameters {
		if bindingUsesAlias(mapping, alias) {
			return true
		}
	}
	for _, mapping := range b.physicalBindings {
		if bindingUsesAlias(mapping, alias) {
			return true
		}
	}
	return false
}
func bindingUsesAlias(mapping Binding, alias string) bool {
	if mapping.timestampRemainder != nil && len(mapping.timestampRemainder.parts) > 1 && mapping.timestampRemainder.parts[0] == alias {
		return true
	}
	if mapping.metadata != nil && len(mapping.metadata.parts) > 1 && mapping.metadata.parts[0] == alias {
		return true
	}
	if len(mapping.column.parts) > 1 && mapping.column.parts[0] == alias {
		return true
	}
	if mapping.structure != nil {
		for _, predicate := range []SQLPredicate{mapping.structure.present, mapping.structure.null} {
			for _, ref := range predicate.references {
				if len(ref.parts) > 0 && ref.parts[0] == alias {
					return true
				}
			}
		}
		for _, field := range mapping.structure.fields {
			if bindingUsesAlias(field, alias) {
				return true
			}
		}
	}
	return false
}

// Provenance does not change how a JSON callback reads its materialized item.
// It only retains enough physical information to reject lossy serialization.
func (b *builder) callbackProvenance(id hff.BindingID, receiver hff.Node) func() {
	if b.parameterProvenance == nil {
		b.parameterProvenance = make(map[hff.BindingID]Binding)
	}
	old, existed := b.parameterProvenance[id]
	mapping, ok := b.relationMapping(receiver, b.physicalParameters)
	if !ok {
		mapping, ok = b.relationMapping(receiver, b.parameterProvenance)
	}
	if ok && mapping.relation != nil {
		b.parameterProvenance[id] = mapping.relation.config.item
	}
	return func() {
		if existed {
			b.parameterProvenance[id] = old
		} else {
			delete(b.parameterProvenance, id)
		}
	}
}
func (b *builder) bindingLosesFloat(mapping Binding, typ hff.Type) bool {
	if !jsonHasFloat(typ) {
		return false
	}
	if mapping.relation != nil {
		return relationLosesFloat(*mapping.relation)
	}
	if mapping.structure != nil {
		return structLosesFloat(mapping, typ, nil)
	}
	return mapping.format == JSONB && mapping.metadata == nil
}
