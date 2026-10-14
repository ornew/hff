package sql

import (
	"strings"

	"github.com/ornew/hff"
)

type jsonParameter struct {
	code      string
	format    JSONFormat
	typ       hff.Type
	lossy     bool
	itemRoles *jsonItemRoles
}

// Callback projections belong to their element subquery. Captures retain the
// enclosing binding IDs and can reference outer lateral projections.
func (b *builder) jsonPredicate(n hff.Node, name string) (expression, error) {
	if value, handled, err := b.relationPredicate(n, name); handled {
		return value, err
	}
	receiver, _ := n.Child(0)
	value, err := b.expression(receiver)
	if err != nil {
		return expression{}, err
	}
	if value.kind == hff.NullType {
		return value, nil
	}
	value, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return expression{}, err
	}
	callback, _ := n.Child(1)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 1 {
		return expression{}, b.failure(n, "collection predicate requires one callback parameter")
	}
	alias := b.nextAlias()
	outerSteps := b.steps
	outerPaths := b.sharedPaths
	b.steps = nil
	b.sharedPaths = nil
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	id := parameters[0].ID()
	receiverLoss := b.losesFloat(receiver, nil)
	restoreProvenance := b.callbackProvenance(id, receiver)
	defer restoreProvenance()
	b.parameters[id] = jsonParameter{code: alias + ".value", format: format, typ: jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true), lossy: receiverLoss, itemRoles: b.jsonItemsRoles(receiver)}
	defer func() { b.steps = outerSteps; b.sharedPaths = outerPaths; delete(b.parameters, id) }()
	body, _ := callback.Child(0)
	result, err := b.expression(body)
	if err != nil {
		return expression{}, err
	}
	predicate := b.truth(body, result)
	operator := "EXISTS"
	if name == "all" {
		operator = "NOT EXISTS"
		predicate = "(NOT " + predicate + ")"
	}
	from := string(format) + "_array_elements(" + value.code + ") AS " + alias + "(value)"
	if len(b.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	}
	if name == "count" {
		code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::bigint ELSE (SELECT count(*) FROM " + from + " WHERE " + predicate + ") END)"
		return expression{code: code, present: value.present, kind: hff.IntType}, nil
	}
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL ELSE " + operator + " (SELECT 1 FROM " + from + " WHERE " + predicate + ") END)"
	return expression{code: code, present: value.present, kind: hff.BoolType}, nil
}
