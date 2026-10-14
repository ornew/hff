package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Sum integer values through PostgreSQL numeric so intermediate totals cannot
// overflow bigint. A later slice adds Float and dynamic numeric projections.
func (b *builder) jsonSum(n hff.Node) (expression, error) {
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
	stored := sortListType(b.jsonStoredType(receiver))
	if stored.Kind() != hff.ListType {
		return expression{}, b.failure(n, "sum requires a recoverable list schema")
	}
	itemType := stored.Element()
	var from, term, present, ordinal string
	if n.ChildCount() == 1 {
		if !integerSumType(itemType) {
			return expression{}, b.failure(n, "PostgreSQL sum currently requires Int list elements")
		}
		alias := b.nextAlias()
		from = string(format) + "_array_elements(" + value.code + ") AS " + alias + "(value)"
		term = jsonScalarCast("("+alias+".value #>> '{}'::text[])", hff.IntType)
		present = "TRUE"
	} else {
		callback, _ := n.Child(1)
		var computed expression
		var projected hff.Type
		from, ordinal, _, _, computed, projected, err = b.jsonCallbackRows(n, value, format, callback, itemType, false)
		if err != nil {
			return expression{}, err
		}
		if !integerSumType(projected) || computed.kind != hff.IntType && computed.kind != hff.NullType {
			return expression{}, b.failure(n, "PostgreSQL sum currently requires an Int callback result")
		}
		term, present = computed.code, computed.present
		if computed.kind == hff.NullType {
			term = "NULL::bigint"
		}
		// A captured outer SQL column alone would make PostgreSQL assign SUM
		// to the outer query. Keep the local array row in the aggregate input.
		present = both(present, ordinal+">0")
	}
	if strings.TrimSpace(term) == "" {
		return expression{}, b.failure(n, "sum has no numeric projection")
	}
	// SUM(bigint) returns numeric. Check the final total before narrowing it
	// to int64, including the empty/all-skipped Int zero.
	total := "COALESCE(sum(CASE WHEN " + present + " THEN " + term + " END),0::numeric)"
	alias := b.nextAlias()
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL::bigint ELSE (SELECT CASE WHEN " + alias + ".total BETWEEN '-9223372036854775808'::numeric AND '9223372036854775807'::numeric THEN " + alias + ".total::bigint ELSE NULL::bigint END FROM (SELECT " + total + " AS total FROM " + from + ") AS " + alias + ") END)"
	return expression{code: code, present: value.present, kind: hff.IntType}, nil
}

func integerSumType(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.IntType, hff.NullType, hff.NeverType:
		return true
	case hff.UnionType:
		for _, member := range typ.Members() {
			if !integerSumType(member) {
				return false
			}
		}
		return true
	}
	return false
}
