package sql

import (
	"fmt"
	"time"

	"github.com/ornew/hff"
)

// A direct Timestamp predicate keeps the physical timestamptz and optional
// nanosecond remainder separate. Value expressions still use the exact numeric
// carrier, including when a guard or a structured parent changes presence.
func (b *builder) directTimestampBinding(n hff.Node) (Binding, bool) {
	if !timestampSchema(n.Type()) {
		return Binding{}, false
	}
	if n.Kind() == hff.ParameterNode {
		binding, ok := n.Binding()
		if !ok {
			return Binding{}, false
		}
		mapping, ok := b.physicalParameters[binding.ID()]
		return mapping, ok && mapping.timestamp
	}
	if n.Kind() != hff.VariableNode && n.Kind() != hff.FieldNode {
		return Binding{}, false
	}
	for parent := n; parent.Kind() == hff.FieldNode; {
		parent, _ = parent.Child(0)
		if parent.Kind() != hff.VariableNode && parent.Kind() != hff.FieldNode {
			return Binding{}, false
		}
	}
	path, ok := n.Path()
	if !ok {
		return Binding{}, false
	}
	for _, mapping := range b.physicalBindings {
		if mapping.timestamp && pathKey(mapping.path) == pathKey(path) {
			return mapping, true
		}
	}
	return Binding{}, false
}

func (b *builder) directTimestampComparisonEligible(n hff.Node) bool {
	left, right, _, ok := timestampComparisonOperands(n)
	if !ok {
		return false
	}
	if _, ok := b.directTimestampBinding(left); !ok {
		return false
	}
	if _, ok := b.directTimestampBinding(right); ok {
		return true
	}
	value, constant, err := b.constantValue(right)
	return err == nil && constant && value.Kind() == hff.TimestampKind
}

func timestampComparisonOperands(n hff.Node) (hff.Node, hff.Node, hff.Operator, bool) {
	if n.ChildCount() != 2 {
		return hff.Node{}, hff.Node{}, "", false
	}
	var op hff.Operator
	switch n.Kind() {
	case hff.BinaryNode:
		op, _ = n.Operator()
		switch op {
		case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
		default:
			return hff.Node{}, hff.Node{}, "", false
		}
	case hff.CallNode:
		method, ok := n.Method()
		if !ok {
			return hff.Node{}, hff.Node{}, "", false
		}
		for _, signature := range method.Signatures() {
			if _, custom := signature.Declaration(); custom {
				return hff.Node{}, hff.Node{}, "", false
			}
		}
		switch method.Name() {
		case "before":
			op = hff.Less
		case "after":
			op = hff.Greater
		default:
			return hff.Node{}, hff.Node{}, "", false
		}
	default:
		return hff.Node{}, hff.Node{}, "", false
	}
	left, _ := n.Child(0)
	right, _ := n.Child(1)
	return left, right, op, true
}

func (b *builder) directTimestampComparison(n hff.Node) (string, error) {
	left, right, op, _ := timestampComparisonOperands(n)
	leftBinding, _ := b.directTimestampBinding(left)
	leftInstant, _ := quoteColumn(leftBinding.column)
	leftRemainder := "0::smallint"
	if leftBinding.timestampRemainder != nil {
		leftRemainder, _ = quoteColumn(*leftBinding.timestampRemainder)
	}

	var rightInstant, rightRemainder string
	rightBinding, physicalRight := b.directTimestampBinding(right)
	if physicalRight {
		rightInstant, _ = quoteColumn(rightBinding.column)
		rightRemainder = "0::smallint"
		if rightBinding.timestampRemainder != nil {
			rightRemainder, _ = quoteColumn(*rightBinding.timestampRemainder)
		}
	} else {
		value, _, err := b.constantValue(right)
		if err != nil {
			return "", err
		}
		stamp, _ := value.AsTimestamp()
		remainder := stamp.UTC().Nanosecond() % 1000
		if leftBinding.timestampRemainder == nil && remainder != 0 {
			switch op {
			case hff.Equal:
				return "FALSE", nil
			case hff.NotEqual:
				return "(" + leftInstant + " IS NOT NULL)", nil
			case hff.Less, hff.LessEqual:
				op = hff.LessEqual
			case hff.Greater, hff.GreaterEqual:
				op = hff.Greater
			}
		}
		b.args = append(b.args, postgresTimestampMicrosecond(stamp))
		rightInstant = fmt.Sprintf("($%d::timestamptz)", len(b.args))
		rightRemainder = "0::smallint"
		if leftBinding.timestampRemainder != nil {
			b.args = append(b.args, int64(remainder))
			rightRemainder = fmt.Sprintf("($%d::smallint)", len(b.args))
		}
	}

	operator := string(op)
	if op == hff.NotEqual {
		operator = "<>"
	}
	test := leftInstant + " " + operator + " " + rightInstant
	if leftRemainder != "0::smallint" || rightRemainder != "0::smallint" {
		switch op {
		case hff.Equal:
			test = "(" + test + " AND " + leftRemainder + " = " + rightRemainder + ")"
		case hff.NotEqual:
			test = "(" + test + " OR (" + leftInstant + " = " + rightInstant + " AND " + leftRemainder + " <> " + rightRemainder + "))"
		case hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
			strict := "<"
			if op == hff.Greater || op == hff.GreaterEqual {
				strict = ">"
			}
			test = "(" + leftInstant + " " + strict + " " + rightInstant + " OR (" + leftInstant + " = " + rightInstant + " AND " + leftRemainder + " " + operator + " " + rightRemainder + "))"
		}
	}
	if physicalRight && acceptsKind(right.Type(), hff.NullType) {
		test = rightInstant + " IS NOT NULL AND " + test
	}
	if acceptsKind(left.Type(), hff.NullType) {
		test = leftInstant + " IS NOT NULL AND " + test
	}
	return "(" + test + ")", nil
}

func postgresTimestampMicrosecond(stamp time.Time) string {
	utc := stamp.UTC()
	base := utc.Format("2006-01-02 15:04:05")
	suffix := "+00"
	if utc.Year() == 0 {
		base = "0001" + base[4:]
		suffix += " BC"
	}
	return fmt.Sprintf("%s.%06d%s", base, utc.Nanosecond()/1000, suffix)
}
