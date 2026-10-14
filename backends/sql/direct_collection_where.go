package sql

import "github.com/ornew/hff"

// Empty collection tests and cardinality predicates need neither a decoded
// JSON carrier nor a materialized relation. Keep guards, dynamic types, and
// metadata-backed bindings on the general lowering path.
type directCollectionPlan struct {
	column   string
	receiver hff.Node
	relation bool
	kind     hff.TypeKind
	op       hff.Operator
	right    hff.Node
	count    int64
	mode     string
}

func (b *builder) directCollectionWhereEligible(n hff.Node) bool {
	_, ok := b.directCollectionPlan(n)
	return ok
}

func (b *builder) directCollectionPlan(n hff.Node) (directCollectionPlan, bool) {
	var receiver hff.Node
	plan := directCollectionPlan{}
	switch n.Kind() {
	case hff.BoolNode:
		child, _ := n.Child(0)
		if builtinUnaryScalarMethod(child, "size") {
			receiver, _ = child.Child(0)
		} else {
			receiver = child
		}
		plan.mode = "nonempty"
	case hff.BinaryNode:
		left, _ := n.Child(0)
		plan.right, _ = n.Child(1)
		plan.op, _ = n.Operator()
		if builtinUnaryScalarMethod(left, "size") {
			receiver, _ = left.Child(0)
			value, literal := plan.right.Literal()
			if !literal || value.Kind() != hff.IntKind {
				return directCollectionPlan{}, false
			}
			plan.count, _ = value.AsInt()
			plan.mode = "size"
		} else {
			if plan.op != hff.Equal && plan.op != hff.NotEqual {
				return directCollectionPlan{}, false
			}
			receiver = left
			plan.mode = "empty"
		}
	default:
		return directCollectionPlan{}, false
	}
	if receiver.Kind() != hff.VariableNode && receiver.Kind() != hff.FieldNode {
		return directCollectionPlan{}, false
	}
	for root := receiver; root.Kind() == hff.FieldNode; {
		root, _ = root.Child(0)
		if root.Kind() != hff.VariableNode && root.Kind() != hff.FieldNode {
			return directCollectionPlan{}, false
		}
	}
	plan.kind, _ = jsonKind(receiver.Type())
	if plan.kind != hff.ListType && plan.kind != hff.MapType && plan.kind != hff.StructType {
		return directCollectionPlan{}, false
	}
	if plan.mode == "empty" {
		literalKind := hff.ListNode
		if plan.kind != hff.ListType {
			literalKind = hff.ObjectNode
		}
		if plan.right.Kind() != literalKind || plan.right.ChildCount() != 0 {
			return directCollectionPlan{}, false
		}
	}
	if plan.mode == "size" && !collectionComparisonOperator(plan.op) {
		return directCollectionPlan{}, false
	}
	if path, ok := receiver.Path(); ok {
		key := pathKey(path)
		for _, binding := range b.physicalBindings {
			if pathKey(binding.path) == key && binding.format == JSONB && binding.metadata == nil &&
				binding.structure == nil && binding.relation == nil && !binding.composite {
				plan.column = b.bindings[key]
				if plan.column != "" && (plan.mode != "size" || plan.count == 0 || plan.kind == hff.ListType) {
					return plan, true
				}
			}
		}
	}
	if plan.kind != hff.ListType || plan.mode == "size" && plan.count != 0 {
		return directCollectionPlan{}, false
	}
	if _, ok := b.relationCardinalityBinding(receiver); !ok {
		return directCollectionPlan{}, false
	}
	plan.receiver = receiver
	plan.relation = true
	return plan, true
}

func collectionComparisonOperator(op hff.Operator) bool {
	switch op {
	case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
		return true
	default:
		return false
	}
}

func (b *builder) directCollectionWhere(n hff.Node) (string, error) {
	plan, _ := b.directCollectionPlan(n)
	if plan.relation {
		// No relation state or correlation expression needs rendering when
		// the comparison is false for every cardinality.
		if plan.mode == "size" && plan.op == hff.Less {
			return "FALSE", nil
		}
		if plan.mode == "size" && plan.op == hff.GreaterEqual {
			absent, _, err := b.relationCardinalityAbsent(plan.receiver)
			if err != nil {
				return "", err
			}
			return "(NOT (" + absent + "))", nil
		}
		source, _, err := b.relationCardinalityReceiver(plan.receiver)
		if err != nil {
			return "", err
		}
		present := "(NOT (" + source.absent + "))"
		exists := "EXISTS (SELECT 1 FROM " + source.from + " WHERE (" + source.correlation + "))"
		condition := collectionEmptyCondition(plan, exists)
		if condition == "FALSE" {
			return "FALSE", nil
		}
		if condition == "TRUE" {
			return present, nil
		}
		return "(" + present + " AND " + condition + ")", nil
	}
	column := plan.column
	jsonType, empty := "array", "'[]'::jsonb"
	if plan.kind != hff.ListType {
		jsonType, empty = "object", "'{}'::jsonb"
	}
	// PostgreSQL jsonb null is distinct from SQL NULL. Both are falsy in
	// HFF, including under an outer NOT; the result must be two-valued.
	present := "(" + column + " IS NOT NULL AND jsonb_typeof(" + column + ") = '" + jsonType + "')"
	if plan.mode == "size" && plan.count != 0 {
		value, err := b.literal(plan.right)
		if err != nil {
			return "", err
		}
		// jsonb_array_length rejects JSON null. CASE protects the call even
		// when PostgreSQL reorders Boolean conjuncts.
		return "(CASE WHEN " + present + " THEN jsonb_array_length(" + column + ") " + sqlComparisonOperator(plan.op) + " " + value.code + " ELSE FALSE END)", nil
	}
	condition := column + " <> " + empty
	if plan.mode == "empty" && plan.op == hff.Equal ||
		plan.mode == "size" && (plan.op == hff.Equal || plan.op == hff.LessEqual) {
		condition = column + " = " + empty
	}
	if plan.mode == "size" {
		switch plan.op {
		case hff.GreaterEqual:
			condition = "TRUE"
		case hff.Less:
			condition = "FALSE"
		}
	}
	if condition == "FALSE" {
		return "FALSE", nil
	}
	if condition == "TRUE" {
		return present, nil
	}
	return "(" + present + " AND " + condition + ")", nil
}

func collectionEmptyCondition(plan directCollectionPlan, nonempty string) string {
	if plan.mode == "nonempty" {
		return nonempty
	}
	if plan.mode == "empty" {
		if plan.op == hff.Equal {
			return "(NOT " + nonempty + ")"
		}
		return nonempty
	}
	switch plan.op {
	case hff.Equal, hff.LessEqual:
		return "(NOT " + nonempty + ")"
	case hff.NotEqual, hff.Greater:
		return nonempty
	case hff.GreaterEqual:
		return "TRUE"
	case hff.Less:
		return "FALSE"
	default:
		return "FALSE"
	}
}

func sqlComparisonOperator(op hff.Operator) string {
	if op == hff.NotEqual {
		return "<>"
	}
	return string(op)
}
