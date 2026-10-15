package sql

import (
	"math"
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// Keys belong to this Program and the currently bound row scope only. A key
// describes the complete expression, including ordered children and exact
// literal representations. A nonempty key without a lexical dependency is
// valid for a child, but cannot itself justify sharing a list's output rows.
func (b *builder) deferredRepeatExpressionKey(n hff.Node) (string, bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", false, err
	}
	parts := []string{strconv.Itoa(int(n.Kind())), n.Type().Key(), strconv.FormatBool(n.MayNoValue()), b.pathOrderClass(n), strconv.FormatBool(b.orderBlind[n])}
	bound := false
	switch n.Kind() {
	case hff.LiteralNode:
		value, _ := n.Literal()
		parts = append(parts, strconv.Itoa(int(value.Kind())))
		switch value.Kind() {
		case hff.NullKind:
		case hff.BoolKind:
			x, _ := value.AsBool()
			parts = append(parts, strconv.FormatBool(x))
		case hff.IntKind:
			x, _ := value.AsInt()
			parts = append(parts, strconv.FormatInt(x, 10))
		case hff.FloatKind:
			x, _ := value.AsFloat()
			parts = append(parts, strconv.FormatUint(math.Float64bits(x), 16))
		case hff.StringKind:
			x, _ := value.AsString()
			parts = append(parts, x)
		default:
			return "", false, nil
		}
	case hff.ParameterNode, hff.VariableNode:
		if binding, ok := n.Binding(); ok {
			if _, active := b.parameters[binding.ID()]; !active {
				return "", false, nil
			}
			parts = append(parts, "binding", strconv.FormatUint(uint64(binding.ID()), 10))
			bound = true
		} else if path, ok := n.Path(); ok {
			parts = append(parts, "path", pathKey(path))
		} else {
			return "", false, nil
		}
	case hff.FieldNode:
		field, _ := n.Field()
		parts = append(parts, field)
	case hff.BinaryNode, hff.NullTestNode:
		op, _ := n.Operator()
		parts = append(parts, string(op))
	case hff.TypeGuardNode:
		target, _ := n.Constraint()
		parts = append(parts, target.Key())
	case hff.ObjectNode:
		keys, _ := n.Keys()
		parts = append(parts, pathKey(keys))
	case hff.CallNode:
		method, _ := n.Method()
		parts = append(parts, method.Name())
		for _, signature := range method.Signatures() {
			if _, custom := signature.Declaration(); custom {
				return "", false, nil
			}
			parts = append(parts, signature.ID())
		}
	case hff.IndexNode, hff.GuardNode, hff.NegateNode, hff.BoolNode, hff.NotNode, hff.AndNode, hff.OrNode, hff.ListNode:
	default:
		// Callback binding equivalence and extension purity are not inferred.
		// New IR kinds must receive an explicit proof before they can share.
		return "", false, nil
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		key, usesBinding, err := b.deferredRepeatExpressionKey(child)
		if err != nil || key == "" {
			return "", false, err
		}
		parts = append(parts, key)
		bound = bound || usesBinding
	}
	return deferredRepeatKey(parts), bound, nil
}

// Prefix lengths preserve boundaries without recursively escaping child keys.
// Encoding a JSON string containing another encoded JSON string would grow
// with each wrapper level even when the input tree remains small.
func deferredRepeatKey(parts []string) string {
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(len(part)))
		key.WriteByte(':')
		key.WriteString(part)
	}
	return key.String()
}
