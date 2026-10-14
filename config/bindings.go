package config

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
)

type bindingSpec struct {
	Kind      string         `json:"kind"`
	Path      []string       `json:"path,omitempty"`
	Column    []string       `json:"column,omitempty"`
	Format    string         `json:"format,omitempty"`
	Metadata  []string       `json:"metadata,omitempty"`
	Remainder []string       `json:"remainder,omitempty"`
	Present   *predicateSpec `json:"present,omitempty"`
	Null      *predicateSpec `json:"null,omitempty"`
	Fields    []bindingSpec  `json:"fields,omitempty"`
	Relation  *relationSpec  `json:"relation,omitempty"`
}

type relationSpec struct {
	Table       []string       `json:"table"`
	Alias       string         `json:"alias"`
	Correlation predicateSpec  `json:"correlation"`
	Present     *predicateSpec `json:"present,omitempty"`
	Null        *predicateSpec `json:"null,omitempty"`
	ItemType    typeSpec       `json:"itemType"`
	Item        bindingSpec    `json:"item"`
	Order       []orderSpec    `json:"order,omitempty"`
}

type orderSpec struct {
	Column     []string `json:"column"`
	Type       typeSpec `json:"type"`
	Descending bool     `json:"descending,omitempty"`
	NullsFirst bool     `json:"nullsFirst,omitempty"`
}

type predicateSpec struct {
	Kind     string          `json:"kind"`
	Left     *scalarSpec     `json:"left,omitempty"`
	Right    *scalarSpec     `json:"right,omitempty"`
	Children []predicateSpec `json:"children,omitempty"`
	Value    *bool           `json:"value,omitempty"`
	ID       string          `json:"id,omitempty"`
}

type scalarSpec struct {
	Kind   string       `json:"kind"`
	Column []string     `json:"column,omitempty"`
	Type   *typeSpec    `json:"type,omitempty"`
	Value  *literalSpec `json:"value,omitempty"`
}

type literalSpec struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

func encodeLiteral(v hff.Value) (literalSpec, error) {
	switch v.Kind() {
	case hff.NullKind:
		return literalSpec{Kind: "null"}, nil
	case hff.BoolKind:
		value, _ := v.AsBool()
		return literalSpec{Kind: "bool", Text: strconv.FormatBool(value)}, nil
	case hff.IntKind:
		value, _ := v.AsInt()
		return literalSpec{Kind: "int", Text: strconv.FormatInt(value, 10)}, nil
	case hff.FloatKind:
		value, _ := v.AsFloat()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return literalSpec{}, fmt.Errorf("non-finite SQL predicate literal")
		}
		return literalSpec{Kind: "float", Text: strconv.FormatFloat(value, 'g', -1, 64)}, nil
	case hff.StringKind:
		value, _ := v.AsString()
		if !utf8.ValidString(value) || containsNUL(value) {
			return literalSpec{}, fmt.Errorf("SQL predicate string literal needs valid UTF-8 without NUL")
		}
		return literalSpec{Kind: "string", Text: value}, nil
	case hff.DurationKind:
		value, _ := v.AsDuration()
		return literalSpec{Kind: "duration", Text: strconv.FormatInt(int64(value), 10)}, nil
	default:
		return literalSpec{}, fmt.Errorf("unsupported SQL predicate literal kind")
	}
}

func decodeLiteral(s literalSpec) (hff.Value, error) {
	switch s.Kind {
	case "null":
		if s.Text != "" {
			return hff.Value{}, fmt.Errorf("null literal has text")
		}
		return hff.Null(), nil
	case "bool":
		if s.Text != "true" && s.Text != "false" {
			return hff.Value{}, fmt.Errorf("invalid bool literal")
		}
		return hff.Bool(s.Text == "true"), nil
	case "int":
		value, err := strconv.ParseInt(s.Text, 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != s.Text {
			return hff.Value{}, fmt.Errorf("invalid int literal")
		}
		return hff.Int(value), nil
	case "float":
		value, err := strconv.ParseFloat(s.Text, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || strconv.FormatFloat(value, 'g', -1, 64) != s.Text {
			return hff.Value{}, fmt.Errorf("invalid float literal")
		}
		return hff.Float(value), nil
	case "string":
		if !utf8.ValidString(s.Text) || containsNUL(s.Text) {
			return hff.Value{}, fmt.Errorf("SQL predicate string literal needs valid UTF-8 without NUL")
		}
		return hff.String(s.Text), nil
	case "duration":
		value, err := strconv.ParseInt(s.Text, 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != s.Text {
			return hff.Value{}, fmt.Errorf("invalid duration literal")
		}
		return hff.Duration(time.Duration(value)), nil
	default:
		return hff.Value{}, fmt.Errorf("unknown SQL predicate literal kind %q", s.Kind)
	}
}

func encodeScalar(v sql.ScalarView) (scalarSpec, error) {
	if v.HasLiteral {
		literal, err := encodeLiteral(v.Literal)
		return scalarSpec{Kind: "literal", Value: &literal}, err
	}
	if len(v.Column) == 0 {
		return scalarSpec{}, fmt.Errorf("SQL scalar has no column or literal")
	}
	typ, err := encodeType(v.Type)
	return scalarSpec{Kind: "column", Column: v.Column, Type: &typ}, err
}

func decodeScalar(s scalarSpec) (sql.Scalar, error) {
	switch s.Kind {
	case "literal":
		if s.Value == nil || s.Type != nil || len(s.Column) != 0 {
			return sql.Scalar{}, fmt.Errorf("literal scalar needs only value")
		}
		value, err := decodeLiteral(*s.Value)
		if err != nil {
			return sql.Scalar{}, err
		}
		return sql.Literal(value), nil
	case "column":
		if s.Type == nil || s.Value != nil || len(s.Column) == 0 {
			return sql.Scalar{}, fmt.Errorf("column scalar needs column and type")
		}
		if err := validateColumn(s.Column); err != nil {
			return sql.Scalar{}, err
		}
		typ, err := decodeType(*s.Type)
		if err != nil {
			return sql.Scalar{}, err
		}
		if _, ok := predicateScalarKind(typ); !ok {
			return sql.Scalar{}, fmt.Errorf("unsupported SQL predicate scalar type")
		}
		return sql.ColumnValue(sql.Column(s.Column...), typ), nil
	default:
		return sql.Scalar{}, fmt.Errorf("unknown SQL scalar kind %q", s.Kind)
	}
}

func encodePredicate(p sql.PredicateView, registry Registry) (predicateSpec, error) {
	s := predicateSpec{Kind: p.Kind}
	switch p.Kind {
	case "trusted":
		if err := validateTrustedPredicate(p); err != nil {
			return s, err
		}
		var matches []string
		for id, candidate := range registry.Predicates {
			view := candidate.View()
			if view.Kind == "trusted" && view.TrustedCode == p.TrustedCode && reflect.DeepEqual(view.References, p.References) {
				matches = append(matches, id)
			}
		}
		if len(matches) != 1 {
			return s, fmt.Errorf("trusted SQL predicate needs exactly one registered ID")
		}
		s.ID = matches[0]
	case "bool":
		s.Value = &p.Boolean
	case "equal", "null", "notnull", "value":
		left, err := encodeScalar(p.Left)
		if err != nil {
			return s, err
		}
		s.Left = &left
		if p.Kind == "equal" {
			right, err := encodeScalar(p.Right)
			if err != nil {
				return s, err
			}
			s.Right = &right
		}
	case "and", "or", "not":
		for _, child := range p.Children {
			encoded, err := encodePredicate(child, registry)
			if err != nil {
				return s, err
			}
			s.Children = append(s.Children, encoded)
		}
	default:
		return s, fmt.Errorf("unknown SQL predicate kind %q", p.Kind)
	}
	return s, nil
}

func decodePredicate(s predicateSpec, registry Registry) (sql.SQLPredicate, error) {
	var zero sql.SQLPredicate
	switch s.Kind {
	case "trusted":
		if s.ID == "" || s.Value != nil || s.Left != nil || s.Right != nil || len(s.Children) != 0 {
			return zero, fmt.Errorf("trusted predicate needs only ID")
		}
		predicate, ok := registry.Predicates[s.ID]
		if !ok || predicate.View().Kind != "trusted" {
			return zero, fmt.Errorf("unregistered trusted predicate ID %q", s.ID)
		}
		if err := validateTrustedPredicate(predicate.View()); err != nil {
			return zero, err
		}
		return predicate, nil
	case "bool":
		if s.Value == nil || s.ID != "" || s.Left != nil || s.Right != nil || len(s.Children) != 0 {
			return zero, fmt.Errorf("Boolean predicate needs only value")
		}
		return sql.Boolean(*s.Value), nil
	case "equal", "null", "notnull", "value":
		if s.Left == nil || s.ID != "" || s.Value != nil || len(s.Children) != 0 {
			return zero, fmt.Errorf("SQL predicate needs a left scalar")
		}
		left, err := decodeScalar(*s.Left)
		if err != nil {
			return zero, err
		}
		if s.Kind == "equal" {
			if s.Right == nil {
				return zero, fmt.Errorf("equality needs a right scalar")
			}
			right, err := decodeScalar(*s.Right)
			if err != nil {
				return zero, err
			}
			leftKind, leftOK := predicateScalarKind(left.Type())
			rightKind, rightOK := predicateScalarKind(right.Type())
			if !leftOK || !rightOK || leftKind != rightKind && leftKind != hff.NullType && rightKind != hff.NullType {
				return zero, fmt.Errorf("SQL equality requires matching scalar types")
			}
			return sql.Equal(left, right), nil
		}
		if s.Right != nil {
			return zero, fmt.Errorf("unexpected right scalar")
		}
		switch s.Kind {
		case "null":
			return sql.IsNull(left), nil
		case "notnull":
			return sql.IsNotNull(left), nil
		default:
			kind, ok := predicateScalarKind(left.Type())
			if !ok || kind != hff.BoolType {
				return zero, fmt.Errorf("SQL value predicate requires bool")
			}
			return sql.AsPredicate(left), nil
		}
	case "and", "or", "not":
		if s.ID != "" || s.Value != nil || s.Left != nil || s.Right != nil || len(s.Children) == 0 || s.Kind == "not" && len(s.Children) != 1 {
			return zero, fmt.Errorf("invalid %s predicate children", s.Kind)
		}
		children := make([]sql.SQLPredicate, len(s.Children))
		for i, child := range s.Children {
			var err error
			children[i], err = decodePredicate(child, registry)
			if err != nil {
				return zero, err
			}
		}
		switch s.Kind {
		case "and":
			return sql.And(children...), nil
		case "or":
			return sql.Or(children...), nil
		default:
			return sql.Not(children[0]), nil
		}
	default:
		return zero, fmt.Errorf("unknown SQL predicate kind %q", s.Kind)
	}
}

func encodeBinding(view sql.BindingView, registry Registry) (bindingSpec, error) {
	if view.Invalid {
		return bindingSpec{}, fmt.Errorf("invalid SQL binding option or relation")
	}
	s := bindingSpec{Kind: view.Kind, Path: append([]string(nil), view.Path...)}
	switch view.Kind {
	case "native", "composite":
		s.Column = view.Column
	case "timestamp":
		s.Column, s.Remainder = view.Column, view.TimestampRemainder
	case "json":
		s.Column, s.Format, s.Metadata = view.Column, string(view.Format), view.Metadata
	case "struct":
		present, err := encodePredicate(view.Present, registry)
		if err != nil {
			return s, err
		}
		null, err := encodePredicate(view.Null, registry)
		if err != nil {
			return s, err
		}
		s.Present, s.Null = &present, &null
		for _, field := range view.Fields {
			child, err := encodeBinding(field, registry)
			if err != nil {
				return s, err
			}
			s.Fields = append(s.Fields, child)
		}
	case "relation":
		if view.Relation == nil {
			return s, fmt.Errorf("relation binding has no relation")
		}
		r := view.Relation
		correlation, err := encodePredicate(r.Correlation, registry)
		if err != nil {
			return s, err
		}
		present, err := encodePredicate(r.Present, registry)
		if err != nil {
			return s, err
		}
		null, err := encodePredicate(r.Null, registry)
		if err != nil {
			return s, err
		}
		itemType, err := encodeType(r.ItemType)
		if err != nil {
			return s, err
		}
		item, err := encodeBinding(r.Item, registry)
		if err != nil {
			return s, err
		}
		rs := &relationSpec{Table: r.Table, Alias: r.Alias, Correlation: correlation, Present: &present, Null: &null, ItemType: itemType, Item: item}
		for _, order := range r.Order {
			typ, err := encodeType(order.Type)
			if err != nil {
				return s, err
			}
			rs.Order = append(rs.Order, orderSpec{Column: order.Column, Type: typ, Descending: order.Descending, NullsFirst: order.NullsFirst})
		}
		s.Relation = rs
	default:
		return s, fmt.Errorf("unknown SQL binding kind %q", view.Kind)
	}
	return s, nil
}

func decodeBinding(s bindingSpec, registry Registry) (sql.Binding, error) {
	if err := validatePath(s.Path); err != nil {
		return sql.Binding{}, err
	}
	if len(s.Column) != 0 {
		if err := validateColumn(s.Column); err != nil {
			return sql.Binding{}, err
		}
	}
	if len(s.Metadata) != 0 {
		if err := validateColumn(s.Metadata); err != nil {
			return sql.Binding{}, err
		}
	}
	if len(s.Remainder) != 0 {
		if err := validateColumn(s.Remainder); err != nil {
			return sql.Binding{}, err
		}
	}
	path := hff.Path(s.Path)
	column := sql.Column(s.Column...)
	var binding sql.Binding
	switch s.Kind {
	case "native", "composite", "timestamp", "json":
		if len(s.Column) == 0 || s.Present != nil || s.Null != nil || len(s.Fields) != 0 || s.Relation != nil {
			return binding, fmt.Errorf("%s binding needs one column representation", s.Kind)
		}
		switch s.Kind {
		case "native":
			if s.Format != "" || len(s.Metadata) != 0 || len(s.Remainder) != 0 {
				return binding, fmt.Errorf("native binding has incompatible fields")
			}
			binding = sql.Bind(path, column)
		case "composite":
			if s.Format != "" || len(s.Metadata) != 0 || len(s.Remainder) != 0 {
				return binding, fmt.Errorf("composite binding has incompatible fields")
			}
			binding = sql.BindComposite(path, column)
		case "timestamp":
			if s.Format != "" || len(s.Metadata) != 0 {
				return binding, fmt.Errorf("timestamp binding has incompatible fields")
			}
			if len(s.Remainder) != 0 {
				binding = sql.BindTimestamp(path, column, sql.WithNanosecondRemainder(sql.Column(s.Remainder...)))
			} else {
				binding = sql.BindTimestamp(path, column)
			}
		case "json":
			if s.Format != string(sql.JSON) && s.Format != string(sql.JSONB) || len(s.Remainder) != 0 {
				return binding, fmt.Errorf("JSON binding needs json or jsonb format")
			}
			if len(s.Metadata) != 0 && s.Format != string(sql.JSONB) {
				return binding, fmt.Errorf("type metadata requires jsonb")
			}
			if len(s.Metadata) != 0 {
				binding = sql.BindJSON(path, column, sql.JSONFormat(s.Format), sql.WithTypeMetadata(sql.Column(s.Metadata...)))
			} else {
				binding = sql.BindJSON(path, column, sql.JSONFormat(s.Format))
			}
		}
	case "struct":
		if len(s.Column) != 0 || s.Format != "" || len(s.Metadata) != 0 || len(s.Remainder) != 0 || s.Relation != nil || s.Present == nil || s.Null == nil {
			return binding, fmt.Errorf("struct binding needs state and fields only")
		}
		present, err := decodePredicate(*s.Present, registry)
		if err != nil {
			return binding, err
		}
		null, err := decodePredicate(*s.Null, registry)
		if err != nil {
			return binding, err
		}
		fields := make([]sql.Binding, len(s.Fields))
		seen := map[string]bool{}
		for i, child := range s.Fields {
			if len(child.Path) != 1 || seen[child.Path[0]] {
				return binding, fmt.Errorf("struct field bindings need distinct relative names")
			}
			seen[child.Path[0]] = true
			fields[i], err = decodeBinding(child, registry)
			if err != nil {
				return binding, err
			}
		}
		binding = sql.BindStruct(path, present, null, fields...)
	case "relation":
		if len(s.Column) != 0 || s.Format != "" || len(s.Metadata) != 0 || len(s.Remainder) != 0 || s.Present != nil || s.Null != nil || len(s.Fields) != 0 || s.Relation == nil {
			return binding, fmt.Errorf("relation binding needs relation only")
		}
		r := s.Relation
		if err := validateColumn(r.Table); err != nil {
			return binding, fmt.Errorf("relation table: %w", err)
		}
		if err := validateColumn([]string{r.Alias}); err != nil {
			return binding, fmt.Errorf("relation alias: %w", err)
		}
		correlation, err := decodePredicate(r.Correlation, registry)
		if err != nil {
			return binding, err
		}
		itemType, err := decodeType(r.ItemType)
		if err != nil {
			return binding, err
		}
		item, err := decodeBinding(r.Item, registry)
		if err != nil {
			return binding, err
		}
		if len(r.Item.Path) != 0 {
			return binding, fmt.Errorf("relation item binding path must be empty")
		}
		options := []sql.RelationOption{sql.WithRelationSource(sql.Column(r.Table...), r.Alias), sql.WithCorrelation(correlation), sql.WithRelationItem(itemType, item)}
		if r.Present != nil || r.Null != nil {
			if r.Present == nil || r.Null == nil {
				return binding, fmt.Errorf("relation state needs present and null")
			}
			present, err := decodePredicate(*r.Present, registry)
			if err != nil {
				return binding, err
			}
			null, err := decodePredicate(*r.Null, registry)
			if err != nil {
				return binding, err
			}
			options = append(options, sql.WithRelationState(present, null))
		}
		if len(r.Order) != 0 {
			orders := make([]sql.RelationOrder, len(r.Order))
			for i, order := range r.Order {
				if err := validateColumn(order.Column); err != nil {
					return binding, fmt.Errorf("relation order: %w", err)
				}
				typ, err := decodeType(order.Type)
				if err != nil {
					return binding, err
				}
				if order.Descending {
					orders[i] = sql.Descending(sql.Column(order.Column...), typ)
				} else {
					orders[i] = sql.Ascending(sql.Column(order.Column...), typ)
				}
				if order.NullsFirst {
					orders[i] = sql.NullsFirst(orders[i])
				}
			}
			options = append(options, sql.WithUniqueOrder(orders...))
		}
		relation, err := sql.NewRelation(options...)
		if err != nil {
			return binding, err
		}
		binding = sql.BindRelation(path, relation)
	default:
		return binding, fmt.Errorf("unknown SQL binding kind %q", s.Kind)
	}
	return binding, nil
}

func predicateScalarKind(typ hff.Type) (hff.TypeKind, bool) {
	kind := typ.Kind()
	if kind == hff.UnionType {
		members := typ.Members()
		if len(members) != 2 {
			return hff.InvalidType, false
		}
		if members[0].Kind() == hff.NullType {
			kind = members[1].Kind()
		} else if members[1].Kind() == hff.NullType {
			kind = members[0].Kind()
		} else {
			return hff.InvalidType, false
		}
	}
	switch kind {
	case hff.NullType, hff.BoolType, hff.IntType, hff.FloatType, hff.StringType, hff.DurationType:
		return kind, true
	default:
		return hff.InvalidType, false
	}
}

func validateColumn(parts []string) error {
	if len(parts) == 0 {
		return fmt.Errorf("SQL column needs at least one identifier")
	}
	for _, part := range parts {
		if part == "" || len(part) > 63 || !utf8.ValidString(part) || containsNUL(part) {
			return fmt.Errorf("SQL identifier must contain 1–63 UTF-8 bytes without NUL")
		}
	}
	return nil
}

func validatePath(path []string) error {
	for _, part := range path {
		if !utf8.ValidString(part) || containsNUL(part) {
			return fmt.Errorf("binding path has invalid UTF-8 or NUL")
		}
	}
	return nil
}

func containsNUL(s string) bool {
	for _, ch := range s {
		if ch == 0 {
			return true
		}
	}
	return false
}
