package sql

import "github.com/ornew/hff"

// A key-only callback can read the String slot without restoring values or
// materializing [key,value] pairs. The proof follows lexical bindings, not
// names, and rejects every complete-pair or value-slot observation.
type jsonItemKeyRows struct {
	value        expression
	format       JSONFormat
	alias, from  string
	key, ordinal string
}

func itemKeyBinding(n hff.Node) (hff.BindingID, bool) {
	if n.Kind() != hff.IndexNode {
		return 0, false
	}
	parent, _ := n.Child(0)
	// A source items pair is never null. Content-dependent type guards
	// cannot be discarded: even a key-only result can depend on its value.
	for parent.Kind() == hff.GuardNode {
		parent, _ = parent.Child(0)
	}
	binding, ok := parent.Binding()
	if !ok {
		return 0, false
	}
	index, _ := n.Child(1)
	slot, constant := itemLiteralIndex(index)
	return binding.ID(), constant && (slot == 0 || slot == -2)
}

func observesOnlyItemKeys(n hff.Node, id hff.BindingID) bool {
	if keyID, ok := itemKeyBinding(n); ok && keyID == id {
		return true
	}
	if binding, ok := n.Binding(); ok && binding.ID() == id {
		return false
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if !observesOnlyItemKeys(child, id) {
			return false
		}
	}
	return true
}

func itemKeySource(receiver, callback hff.Node) (hff.Node, bool, bool) {
	guarded := false
	for receiver.Kind() == hff.GuardNode {
		child, _ := receiver.Child(0)
		guarded = guarded || acceptsKind(child.Type(), hff.NullType)
		receiver = child
	}
	method, ok := receiver.Method()
	if !ok || method.Name() != "items" {
		return hff.Node{}, false, false
	}
	if callback.Valid() {
		parameters, ok := callback.Parameters()
		body, _ := callback.Child(0)
		if !ok || len(parameters) != 1 || !observesOnlyItemKeys(body, parameters[0].ID()) {
			return hff.Node{}, false, false
		}
	}
	objectNode, _ := receiver.Child(0)
	return objectNode, guarded, true
}

func (b *builder) jsonItemsKeyRows(receiver, callback hff.Node, ordered bool) (*jsonItemKeyRows, error) {
	objectNode, guarded, eligible := itemKeySource(receiver, callback)
	if !eligible {
		return nil, nil
	}
	value, err := b.expression(objectNode)
	if err != nil {
		return nil, err
	}
	raw, format, err := b.jsonRepresentation(objectNode, value)
	if err != nil {
		return nil, err
	}
	object := b.project("(CASE WHEN " + string(format) + "_typeof(" + raw + ")='object' THEN " + raw + " ELSE NULL::" + string(format) + " END)")
	present := value.present
	if guarded {
		present = both(present, "("+object+" IS NOT NULL)")
	}
	alias := b.nextAlias()
	from := string(format) + "_object_keys(" + object + ") AS " + alias + "(key)"
	ordinal := ""
	if ordered {
		rows := b.nextAlias()
		from = "(SELECT " + alias + ".key,row_number() OVER(ORDER BY " + alias + ".key COLLATE \"C\") AS ord FROM " + from + ") AS " + rows
		alias = rows
		ordinal = alias + ".ord"
	}
	return &jsonItemKeyRows{
		value:  expression{code: object, present: present, kind: hff.ListType},
		format: format, alias: alias, from: from, key: alias + ".key", ordinal: ordinal,
	}, nil
}

// Both paths preserve the collection's null/no-value state. The key path's
// private receiver carrier is an object; only its row source observes it.
func (b *builder) jsonCollectionReceiver(n hff.Node, ordered bool) (expression, JSONFormat, *jsonItemKeyRows, error) {
	receiver, _ := n.Child(0)
	if n.ChildCount() > 1 {
		callback, _ := n.Child(1)
		keys, err := b.jsonItemsKeyRows(receiver, callback, ordered)
		if err != nil {
			return expression{}, "", nil, err
		}
		if keys != nil {
			return keys.value, keys.format, keys, nil
		}
	}
	value, err := b.expression(receiver)
	if err != nil || value.kind == hff.NullType {
		return value, "", nil, err
	}
	value, format, err := b.jsonCallbackReceiver(n, value)
	return value, format, nil, err
}

func (b *builder) jsonItemsKeySize(n hff.Node) (expression, bool, error) {
	receiver, _ := n.Child(0)
	keys, err := b.jsonItemsKeyRows(receiver, hff.Node{}, false)
	if err != nil || keys == nil {
		return expression{}, keys != nil || err != nil, err
	}
	code := "(CASE WHEN " + keys.value.code + " IS NULL THEN NULL::bigint ELSE (SELECT count(*) FROM " + keys.from + ") END)"
	return expression{code: code, present: keys.value.present, kind: hff.IntType}, true, nil
}
