package sql

import (
	"strings"

	"github.com/ornew/hff"
)

type jsonParameter struct {
	code          string
	format        JSONFormat
	typ           hff.Type
	lossy         bool
	itemRoles     *jsonItemRoles
	itemListRoles *jsonItemRoles
	itemKey       string
	direct        *expression
	declared      bool // schema-valid stored field; raw observers need no scalar round trip
	itemSlot      *jsonItemSlot
	deferred      *jsonDeferredValue
}

// Callback projections belong to their element subquery. Captures retain the
// enclosing binding IDs and can reference outer lateral projections.
func (b *builder) jsonPredicate(n hff.Node, name string) (expression, error) {
	if value, handled, err := b.relationPredicate(n, name); handled {
		return value, err
	}
	if value, handled, err := b.jsonMapPipeline(n, name); handled {
		return value, err
	}
	receiver, _ := n.Child(0)
	value, format, keys, err := b.jsonCollectionReceiver(n, false)
	if err != nil {
		return expression{}, err
	}
	if value.kind == hff.NullType {
		return value, nil
	}
	callback, _ := n.Child(1)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 1 {
		return expression{}, b.failure(n, "collection predicate requires one callback parameter")
	}
	alias := b.nextAlias()
	if keys != nil {
		alias = keys.alias
	}
	outerSteps := b.steps
	outerPaths := b.sharedPaths
	b.steps = nil
	b.sharedPaths = nil
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	id := parameters[0].ID()
	previous, existed := b.parameters[id]
	receiverLoss := b.losesFloat(receiver, nil)
	restoreProvenance := b.callbackProvenance(id, receiver)
	defer restoreProvenance()
	b.parameters[id] = jsonParameter{code: alias + ".value", format: format, typ: jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true), lossy: receiverLoss, itemRoles: b.jsonItemsRoles(receiver)}
	if keys != nil {
		parameter := b.parameters[id]
		parameter.itemKey = keys.key
		b.parameters[id] = parameter
	}
	defer func() {
		b.steps = outerSteps
		b.sharedPaths = outerPaths
		if existed {
			b.parameters[id] = previous
		} else {
			delete(b.parameters, id)
		}
	}()
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
	if keys != nil {
		from = keys.from
	}
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
