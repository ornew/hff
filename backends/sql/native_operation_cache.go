package sql

import (
	"strconv"

	"github.com/ornew/hff"
)

type operationOperand struct {
	node           hff.Node
	value          expression
	argumentsAdded int
}

// Operands have already been lowered in source order. A cache hit therefore
// cannot hide validation errors. Constants use their validated values rather
// than a newly allocated placeholder; other carriers must add no arguments on
// a hit. A miss may record a newly computed operand for a later nested hit.
// The existing sharedPaths resets keep these results inside their row scope.
func (b *builder) reuseNativeOperation(n hff.Node, operation string, argumentsStart int, operands ...operationOperand) (expression, bool) {
	parts := []string{operation, n.Type().Key(), b.pathOrderClass(n)}
	discardableArguments := true
	for _, operand := range operands {
		key, discardable := b.nativeOperationOperandKey(operand)
		if key == "" {
			return expression{}, false
		}
		discardableArguments = discardableArguments && discardable
		parts = append(parts, key)
	}
	key := "native-operation:" + pathKey(parts)
	if cached, exists := b.sharedPaths[key]; exists {
		if !discardableArguments {
			return expression{}, false
		}
		// Every new argument belongs to a validated literal operand. No new
		// SQL uses these placeholders: the surviving computation belongs to
		// the first occurrence, including its original ordered arguments.
		b.args = b.args[:argumentsStart]
		b.markIdentityAlias(n)
		return cached, true
	}
	if b.operationKeys == nil {
		b.operationKeys = make(map[hff.Node]string)
	}
	// expression() records the final value/presence alias after projection.
	// Store no unprojected computation in the scope cache.
	b.operationKeys[n] = key
	return expression{}, false
}

func (b *builder) nativeOperationOperandKey(operand operationOperand) (string, bool) {
	value := operand.value
	parts := []string{strconv.Itoa(int(value.kind)), value.present, operand.node.Type().Key()}
	// Only literals and conversions already folded by ordinary lowering can
	// replace their fresh placeholder. Do not evaluate another expression (or
	// an extension) merely to decide whether it is a constant cache input.
	if operand.argumentsAdded <= 1 && (operand.node.Kind() == hff.LiteralNode || b.literalConversions[operand.node].handled) {
		constant, known, err := b.constantValue(operand.node)
		if err != nil || !known {
			return "", false
		}
		if integer, ok := constant.AsInt(); ok && value.kind == hff.IntType {
			return pathKey(append(parts, "literal-int", strconv.FormatInt(integer, 10))), true
		}
		if text, ok := constant.AsString(); ok && value.kind == hff.StringType {
			return pathKey(append(parts, "literal-string", text)), true
		}
	}
	return pathKey(append(parts, "carrier", value.code)), operand.argumentsAdded == 0
}
