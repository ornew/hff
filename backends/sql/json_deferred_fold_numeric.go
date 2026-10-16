package sql

import "github.com/ornew/hff"

// A native leaf must already have the same single numeric storage family.
// Validating an old packet alone cannot prove that its normalization was an
// identity operation for Any, a wider union, or a mismatched native kind.
func (b *builder) nativeNumericDeferredFoldParameter(parameter jsonParameter) (jsonParameter, bool) {
	value := deferredLeaf(parameter)
	if value.leaf == nil {
		return jsonParameter{}, false
	}
	p := *value.leaf
	if p.direct == nil || p.format != "" || p.itemSlot != nil || p.itemRoles != nil || p.itemListRoles != nil || p.itemKey != "" || b.jsonValues[p.direct.code] != "" {
		return jsonParameter{}, false
	}
	kind, ok := jsonKind(p.typ)
	if !ok || kind != p.direct.kind || kind != hff.IntType && kind != hff.FloatType {
		return jsonParameter{}, false
	}
	return p, true
}

func (b *builder) nativeNumericDeferredFoldInputs(n hff.Node, plan jsonDeferredList) (bool, error) {
	if b.disableDeferredFoldPruning {
		return false, nil
	}
	found := false
	for _, arm := range plan.arms {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if arm.empty {
			continue
		}
		if _, ok := b.nativeNumericDeferredFoldParameter(arm.value); !ok {
			return false, nil
		}
		found = true
	}
	if !found {
		return false, nil
	}
	return b.builtinDeferredFoldProjection(n)
}

// JSON remains the accumulator carrier. Only already-native numeric input
// leaves use separate typed columns, so UNION cannot coerce an Int to Float.
// Empty validation arms retain their original JSON packet and decoder.
type jsonDeferredFoldTransport struct {
	packet jsonDeferredPacket
	native *jsonParameter
}

func (t jsonDeferredFoldTransport) nativeColumn() string {
	if t.native.direct.kind == hff.IntType {
		return "int_value"
	}
	return "float_value"
}

func (t jsonDeferredFoldTransport) columns() string {
	if t.native == nil {
		return t.packet.code + " AS value"
	}
	if t.native.direct.kind == hff.IntType {
		return "(" + t.native.direct.code + ")::bigint AS int_value,NULL::double precision AS float_value"
	}
	return "NULL::bigint AS int_value,(" + t.native.direct.code + ")::double precision AS float_value"
}

func (t jsonDeferredFoldTransport) decode(b *builder, n hff.Node, code string) (jsonParameter, error) {
	if t.native != nil {
		// Do not retain code, presence, normalization proofs or aliases from
		// the source scope. Its no-value state belongs to the source plan.
		value := expression{code: code, present: "TRUE", kind: t.native.direct.kind}
		return jsonParameter{typ: t.native.typ, lossy: t.native.lossy, direct: &value}, checkContext(b.ctx)
	}
	value, err := t.packet.decode(b, n, code)
	if err != nil {
		return jsonParameter{}, err
	}
	return deferredValueParameter(value), nil
}
