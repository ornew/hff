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
	absent := []string{"(" + c.present.code + ") IS NOT TRUE", "(" + c.null.code + ") IS TRUE"}
	parent := receiver
	for parent.Kind() == hff.FieldNode {
		parent, _ = parent.Child(0)
		if ancestor, ok := b.relationMapping(parent, b.physicalParameters); ok && ancestor.structure != nil {
			absent = append(absent, "("+ancestor.structure.present.code+") IS NOT TRUE", "("+ancestor.structure.null.code+") IS TRUE")
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
	code := "(CASE WHEN " + strings.Join(absent, " OR ") + " THEN NULL ELSE " + operator + " (SELECT 1 FROM " + from + " WHERE (" + c.correlation.code + ") AND " + predicate + ") END)"
	return expression{code: code, present: "TRUE", kind: hff.BoolType}, true, nil
}
func (b *builder) physicalItemJSON(n hff.Node, mapping Binding, typ hff.Type) (string, JSONFormat, error) {
	switch {
	case mapping.structure != nil:
		code, err := b.structJSON(n, mapping, typ)
		return code, JSON, err
	case mapping.relation != nil:
		code, err := b.relationJSON(n, mapping)
		return code, JSON, err
	case mapping.format != "":
		code, err := quoteColumn(mapping.column)
		return code, mapping.format, err
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
	for _, mapping := range b.structBindings {
		if bindingUsesAlias(mapping, alias) {
			return true
		}
	}
	return false
}
func bindingUsesAlias(mapping Binding, alias string) bool {
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
	return mapping.format == JSONB
}
