package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Scalar is an immutable SQL scalar expression for one supported physical
// type: null, Boolean, int64, float64, or string, optionally unioned with HFF null.
// Equality requires the same non-null scalar kind (comparison with a null
// literal is allowed); Build does not promote mixed int/float SQL values. A
// Scalar may be reused across Builds.
type Scalar struct {
	column  *ColumnRef
	literal *hff.Value
	typ     hff.Type
}

// ColumnValue declares a physical column and its HFF scalar type. Build quotes
// every identifier; the caller guarantees the database column and data match
// that type. For example, ColumnValue(Column("orders", "tenant_id"),
// hff.PrimitiveType(hff.IntType)) describes an int64 column.
func ColumnValue(column ColumnRef, typ hff.Type) Scalar {
	owned := Column(column.parts...)
	return Scalar{column: &owned, typ: typ}
}

// Literal creates a typed scalar value. Build binds non-null values in
// Query.Args; explicit null becomes SQL NULL. Unsupported values are rejected
// during Build. Supported values are null, bool, int64,
// finite float64, and string; bytes, timestamps, and durations are not SQL
// predicate scalars in the current dialect profile.
func Literal(value hff.Value) Scalar {
	typ := hff.Type{}
	switch value.Kind() {
	case hff.NullKind:
		typ = hff.PrimitiveType(hff.NullType)
	case hff.BoolKind:
		typ = hff.PrimitiveType(hff.BoolType)
	case hff.IntKind:
		typ = hff.PrimitiveType(hff.IntType)
	case hff.FloatKind:
		typ = hff.PrimitiveType(hff.FloatType)
	case hff.StringKind:
		typ = hff.PrimitiveType(hff.StringType)
	}
	return Scalar{literal: &value, typ: typ}
}

// Type returns the declared HFF type of this SQL scalar expression.
func (s Scalar) Type() hff.Type { return s.typ }

type predicateNode struct {
	kind        string
	left, right Scalar
	children    []SQLPredicate
	constant    bool
}

// Equal constructs SQL equality between matching scalar kinds. int64 and
// float64 are not implicitly promoted. For example, use Equal(ColumnValue(...),
// Literal(hff.Int(17))) to compare a declared int64 column with a bound value.
// SQL NULL yields unknown, not an HFF null-identity comparison; use IsNull or
// IsNotNull to test SQL null.
func Equal(left, right Scalar) SQLPredicate {
	return SQLPredicate{references: scalarReferences(left, right), node: &predicateNode{kind: "equal", left: left, right: right}}
}

// IsNull constructs a physical SQL NULL test for value.
func IsNull(value Scalar) SQLPredicate {
	return SQLPredicate{references: scalarReferences(value), node: &predicateNode{kind: "null", left: value}}
}

// IsNotNull constructs a physical SQL non-NULL test for value.
func IsNotNull(value Scalar) SQLPredicate {
	return SQLPredicate{references: scalarReferences(value), node: &predicateNode{kind: "notnull", left: value}}
}

// Boolean constructs a fixed SQL TRUE/FALSE predicate without raw SQL.
func Boolean(value bool) SQLPredicate {
	return SQLPredicate{node: &predicateNode{kind: "bool", constant: value}}
}

// AsPredicate uses a Boolean scalar as a SQL predicate. It preserves SQL's
// three-valued NULL behavior; it does not coerce SQL NULL to false. Use it only
// where SQL truth semantics are the intended predicate semantics.
func AsPredicate(value Scalar) SQLPredicate {
	return SQLPredicate{references: scalarReferences(value), node: &predicateNode{kind: "value", left: value}}
}

// And combines predicates using SQL AND semantics. SQL NULL remains unknown.
func And(predicates ...SQLPredicate) SQLPredicate {
	return SQLPredicate{references: predicateReferences(predicates...), node: &predicateNode{kind: "and", children: append([]SQLPredicate(nil), predicates...)}}
}

// Or combines predicates using SQL OR semantics. SQL NULL remains unknown.
func Or(predicates ...SQLPredicate) SQLPredicate {
	return SQLPredicate{references: predicateReferences(predicates...), node: &predicateNode{kind: "or", children: append([]SQLPredicate(nil), predicates...)}}
}

// Not negates a predicate using SQL NOT semantics, preserving unknown.
func Not(predicate SQLPredicate) SQLPredicate {
	return SQLPredicate{references: predicateReferences(predicate), node: &predicateNode{kind: "not", children: []SQLPredicate{predicate}}}
}

func (b *builder) predicateCode(predicate SQLPredicate) string {
	if predicate.node != nil {
		code, err := b.compilePredicate(predicate)
		if err != nil {
			b.predicateFailure = err
		}
		return code
	}
	return predicate.code
}
func (b *builder) predicateScalar(value Scalar) (expression, error) {
	if err := checkContext(b.ctx); err != nil {
		return expression{}, err
	}
	if err := value.typ.Check(); err != nil {
		return expression{}, configuration("invalid SQL scalar type")
	}
	kind, ok := scalarKind(value.typ)
	if !ok {
		return expression{}, configuration("SQL predicate requires a supported scalar type")
	}
	if value.column != nil {
		column, err := quoteColumn(*value.column)
		if err != nil {
			return expression{}, err
		}
		for _, part := range value.column.parts {
			b.reserved[part] = true
		}
		return expression{column, "TRUE", kind}, nil
	}
	if value.literal != nil {
		return b.parameter(b.program.Root(), *value.literal)
	}
	return expression{}, configuration("invalid SQL scalar expression")
}
func (b *builder) compilePredicate(predicate SQLPredicate) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if predicate.node == nil {
		if err := b.checkPredicate(predicate); err != nil {
			return "", err
		}
		return predicate.code, nil
	}
	if code, ok := b.predicates[predicate.node]; ok {
		return code, nil
	}
	node := predicate.node
	var code string
	switch node.kind {
	case "bool":
		code = "FALSE"
		if node.constant {
			code = "TRUE"
		}
	case "equal", "null", "notnull", "value":
		left, err := b.predicateScalar(node.left)
		if err != nil {
			return "", err
		}
		switch node.kind {
		case "equal":
			right, err := b.predicateScalar(node.right)
			if err != nil {
				return "", err
			}
			if left.kind != right.kind && left.kind != hff.NullType && right.kind != hff.NullType {
				return "", configuration("SQL equality requires matching scalar types")
			}
			code = "(" + left.code + " = " + right.code + ")"
		case "null":
			code = "(" + left.code + " IS NULL)"
		case "notnull":
			code = "(" + left.code + " IS NOT NULL)"
		case "value":
			if left.kind != hff.BoolType {
				return "", configuration("SQL predicate requires bool")
			}
			code = left.code
		}
	case "and", "or", "not":
		if len(node.children) == 0 {
			return "", configuration("SQL AND/OR requires at least one predicate")
		}
		var children []string
		for _, child := range node.children {
			part, err := b.compilePredicate(child)
			if err != nil {
				return "", err
			}
			children = append(children, "("+part+")")
		}
		if node.kind == "not" {
			code = "(NOT " + children[0] + ")"
		} else {
			operator := " AND "
			if node.kind == "or" {
				operator = " OR "
			}
			code = "(" + strings.Join(children, operator) + ")"
		}
	default:
		return "", configuration("invalid SQL predicate")
	}
	if b.predicates == nil {
		b.predicates = make(map[*predicateNode]string)
	}
	b.predicates[predicate.node] = code
	return code, nil
}

func scalarReferences(values ...Scalar) []ColumnRef {
	var refs []ColumnRef
	for _, value := range values {
		if value.column != nil {
			refs = append(refs, *value.column)
		}
	}
	return refs
}
func predicateReferences(values ...SQLPredicate) []ColumnRef {
	var refs []ColumnRef
	for _, value := range values {
		refs = append(refs, value.references...)
	}
	return refs
}
