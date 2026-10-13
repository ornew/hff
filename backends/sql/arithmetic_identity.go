package sql

import (
	"math"

	"github.com/ornew/hff"
)

type arithmeticIdentity struct {
	operand hff.Node
	kind    hff.TypeKind
}

// identityPlan removes only a present, validated numeric constant. The other
// operand remains evaluated, including its null, no-value and scope behavior.
// Dynamic numeric unions keep their family checks on the ordinary path.
func (b *builder) identityPlan(n hff.Node) (arithmeticIdentity, bool) {
	if n.Kind() != hff.BinaryNode {
		return arithmeticIdentity{}, false
	}
	op, _ := n.Operator()
	if op != hff.Add && op != hff.Subtract && op != hff.Multiply && op != hff.Divide {
		return arithmeticIdentity{}, false
	}
	l, _ := n.Child(0)
	r, _ := n.Child(1)
	try := func(operand, constant hff.Node, constantLeft bool) (arithmeticIdentity, bool) {
		kind, ok := computedScalarKind(operand.Type())
		if !ok || (kind != hff.IntType && kind != hff.FloatType) {
			// A closed conversion can resolve a numeric union to one exact
			// constant family, without admitting any runtime family.
			value, known, err := b.constantValue(operand)
			if err != nil || !known {
				return arithmeticIdentity{}, false
			}
			switch value.Kind() {
			case hff.IntKind:
				kind = hff.IntType
			case hff.FloatKind:
				kind = hff.FloatType
			default:
				return arithmeticIdentity{}, false
			}
		}
		value, known, err := b.constantValue(constant)
		if err != nil || !known {
			// Leave failures to ordinary source-order lowering.
			return arithmeticIdentity{}, false
		}
		zero, one, negativeZero, floating := false, false, false, false
		switch value.Kind() {
		case hff.IntKind:
			v, _ := value.AsInt()
			zero, one = v == 0, v == 1
		case hff.FloatKind:
			v, _ := value.AsFloat()
			zero, one, negativeZero, floating = v == 0, v == 1, math.Signbit(v) && v == 0, true
		default:
			return arithmeticIdentity{}, false
		}
		allowed := false
		switch op {
		case hff.Add:
			// Int cannot carry negative zero; Float plus zero can change it.
			allowed = zero && kind == hff.IntType
		case hff.Subtract:
			allowed = !constantLeft && zero && (kind == hff.IntType || !negativeZero)
		case hff.Multiply:
			allowed = one
		case hff.Divide:
			allowed = !constantLeft && one
		}
		if !allowed {
			return arithmeticIdentity{}, false
		}
		if floating || op == hff.Divide {
			kind = hff.FloatType
		}
		return arithmeticIdentity{operand, kind}, true
	}
	if identity, ok := try(l, r, false); ok {
		return identity, true
	}
	if op == hff.Add || op == hff.Multiply {
		return try(r, l, true)
	}
	return arithmeticIdentity{}, false
}

func (b *builder) lowerIdentity(n hff.Node, identity arithmeticIdentity) (expression, error) {
	value, err := b.expression(identity.operand)
	if err != nil {
		return expression{}, err
	}
	if identity.kind == hff.FloatType && value.kind == hff.IntType {
		value.code = "(" + value.code + "::double precision)"
		value.kind = hff.FloatType
	} else {
		// This expression already belongs to the surviving operand's scope.
		// Do not add an identical value/presence projection around it.
		if b.identityAliases == nil {
			b.identityAliases = make(map[hff.Node]bool)
		}
		b.identityAliases[n] = true
	}
	return value, nil
}
