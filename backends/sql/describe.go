package sql

import "github.com/ornew/hff"

// ScalarView is a defensive description of a typed SQL scalar. Exactly one of
// Column and HasLiteral is set for a valid scalar.
type ScalarView struct {
	Column     []string
	Literal    hff.Value
	HasLiteral bool
	Type       hff.Type
}

// View describes the scalar without exposing its mutable internal storage.
func (s Scalar) View() ScalarView {
	view := ScalarView{Type: s.typ}
	if s.column != nil {
		view.Column = append([]string(nil), s.column.parts...)
	}
	if s.literal != nil {
		view.Literal, view.HasLiteral = *s.literal, true
	}
	return view
}

// PredicateView describes a typed predicate tree or a trusted SQL expression.
// TrustedCode is developer-authored SQL. A serializer must replace it with an
// ID from a trusted registry rather than writing that text to an untrusted
// configuration document.
type PredicateView struct {
	Kind        string
	Left        ScalarView
	Right       ScalarView
	Children    []PredicateView
	Boolean     bool
	TrustedCode string
	References  [][]string
}

// View returns a recursive, detached description of p.
func (p SQLPredicate) View() PredicateView {
	if p.node == nil {
		view := PredicateView{Kind: "trusted", TrustedCode: p.code}
		for _, reference := range p.references {
			view.References = append(view.References, append([]string(nil), reference.parts...))
		}
		return view
	}
	view := PredicateView{Kind: p.node.kind, Left: p.node.left.View(), Right: p.node.right.View(), Boolean: p.node.constant}
	for _, child := range p.node.children {
		view.Children = append(view.Children, child.View())
	}
	return view
}

// BindingView is a detached description of one physical mapping. Kind is one
// of native, json, composite, timestamp, struct, or relation. Nested paths
// remain relative to their enclosing struct or relation item.
type BindingView struct {
	Kind               string
	Invalid            bool
	Path               hff.Path
	Column             []string
	Format             JSONFormat
	Metadata           []string
	TimestampRemainder []string
	Present            PredicateView
	Null               PredicateView
	Fields             []BindingView
	Relation           *RelationView
}

// RelationView describes a correlated row source and its item mapping. An
// empty Order means order-dependent operations are unavailable.
type RelationView struct {
	Table       []string
	Alias       string
	Correlation PredicateView
	Present     PredicateView
	Null        PredicateView
	ItemType    hff.Type
	Item        BindingView
	Order       []OrderView
}

// OrderView describes one typed component of a relation's unique order.
type OrderView struct {
	Column     []string
	Type       hff.Type
	Descending bool
	NullsFirst bool
}

// View returns a recursive, detached description of the binding. Invalid
// option values remain visible and must be rejected by the caller's usual
// Build validation before a configuration is used.
func (b Binding) View() BindingView {
	view := BindingView{Kind: "native", Invalid: b.invalidJSONOption || b.invalidTimestampOption,
		Path: append(hff.Path(nil), b.path...), Column: append([]string(nil), b.column.parts...)}
	switch {
	case b.timestamp:
		view.Kind = "timestamp"
	case b.composite:
		view.Kind = "composite"
	case b.format != "":
		view.Kind, view.Format = "json", b.format
	case b.structure != nil:
		view.Kind = "struct"
		view.Present, view.Null = b.structure.present.View(), b.structure.null.View()
		for _, child := range b.structure.fields {
			view.Fields = append(view.Fields, child.View())
		}
	case b.relation != nil:
		view.Kind = "relation"
		view.Invalid = view.Invalid || !b.relation.valid
		c := b.relation.config
		relation := &RelationView{Table: append([]string(nil), c.table.parts...), Alias: c.alias,
			Correlation: c.correlation.View(), Present: c.present.View(), Null: c.null.View(),
			ItemType: c.typ, Item: c.item.View()}
		for _, order := range c.order {
			relation.Order = append(relation.Order, OrderView{Column: append([]string(nil), order.column.parts...), Type: order.typ,
				Descending: order.descending, NullsFirst: order.nullsFirst})
		}
		view.Relation = relation
	}
	if b.metadata != nil {
		view.Metadata = append([]string(nil), b.metadata.parts...)
	}
	if b.timestampRemainder != nil {
		view.TimestampRemainder = append([]string(nil), b.timestampRemainder.parts...)
	}
	return view
}
