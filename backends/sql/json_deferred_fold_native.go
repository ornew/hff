package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// A native leaf must already have the same single scalar storage family.
// Validating an old packet alone cannot prove that its normalization was an
// identity operation for Any, a wider union, or a mismatched native kind.
func (b *builder) nativeScalarDeferredFoldParameter(parameter jsonParameter) (jsonParameter, bool) {
	value := deferredLeaf(parameter)
	if value.leaf == nil {
		return jsonParameter{}, false
	}
	p := *value.leaf
	if p.direct == nil || p.format != "" || p.itemSlot != nil || p.itemRoles != nil || p.itemListRoles != nil || p.itemKey != "" || b.jsonValues[p.direct.code] != "" {
		return jsonParameter{}, false
	}
	kind, ok := jsonKind(p.typ)
	if !ok || kind != p.direct.kind {
		return jsonParameter{}, false
	}
	if _, supported := nativeDeferredFoldColumn(kind); !supported {
		return jsonParameter{}, false
	}
	return p, true
}

func (b *builder) nativeScalarDeferredFoldInputs(n hff.Node, plan jsonDeferredList) (bool, error) {
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
		if _, ok := b.nativeScalarDeferredFoldParameter(arm.value); !ok {
			return false, nil
		}
		found = true
	}
	if !found {
		return false, nil
	}
	return b.builtinDeferredFoldProjection(n)
}

type jsonDeferredFoldColumn struct {
	kind      hff.TypeKind
	name, sql string
}

func nativeDeferredFoldColumn(kind hff.TypeKind) (jsonDeferredFoldColumn, bool) {
	column := jsonDeferredFoldColumn{kind: kind}
	switch kind {
	case hff.IntType:
		column.name, column.sql = "int_value", "bigint"
	case hff.FloatType:
		column.name, column.sql = "float_value", "double precision"
	case hff.BoolType:
		column.name, column.sql = "bool_value", "boolean"
	case hff.StringType:
		column.name, column.sql = "string_value", "text"
	case hff.BytesType:
		column.name, column.sql = "bytes_value", "bytea"
	case hff.DurationType:
		column.name, column.sql = "duration_value", "bigint"
	case hff.TimestampType:
		column.name, column.sql = "timestamp_value", "numeric"
	default:
		return jsonDeferredFoldColumn{}, false
	}
	return column, true
}

// Select one shared layout from static arm metadata, including nullable arms.
// Int and Duration have distinct columns despite sharing a physical SQL type;
// Timestamp keeps exact epoch nanoseconds in unconstrained numeric.
func (b *builder) nativeDeferredFoldColumns(plan jsonDeferredList) ([]jsonDeferredFoldColumn, error) {
	kinds := make(map[hff.TypeKind]bool)
	for _, arm := range plan.arms {
		if err := checkContext(b.ctx); err != nil {
			return nil, err
		}
		if !arm.empty {
			kinds[deferredLeaf(arm.value).leaf.direct.kind] = true
		}
	}
	var columns []jsonDeferredFoldColumn
	for _, kind := range []hff.TypeKind{hff.IntType, hff.FloatType, hff.BoolType, hff.StringType, hff.BytesType, hff.DurationType, hff.TimestampType} {
		if kinds[kind] {
			column, _ := nativeDeferredFoldColumn(kind)
			columns = append(columns, column)
		}
	}
	return columns, nil
}

// JSON remains the accumulator carrier. Already-native scalar input leaves
// use separate typed columns, so UNION cannot coerce their physical families.
// Empty validation arms retain their original JSON packet and decoder.
type jsonDeferredFoldTransport struct {
	packet     jsonDeferredPacket
	native     *jsonParameter
	projection *jsonDeferredFoldProjection
}

func (t jsonDeferredFoldTransport) nativeColumn() string {
	column, _ := nativeDeferredFoldColumn(t.native.direct.kind)
	return column.name
}

func (t jsonDeferredFoldTransport) columns(layout []jsonDeferredFoldColumn) string {
	if t.projection != nil {
		return t.projection.columns(layout)
	}
	if t.native == nil {
		return t.packet.code + " AS value"
	}
	columns := make([]string, len(layout))
	for i, column := range layout {
		value := "NULL"
		if column.kind == t.native.direct.kind {
			value = "(" + t.native.direct.code + ")"
		}
		columns[i] = value + "::" + column.sql + " AS " + column.name
	}
	return strings.Join(columns, ",")
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
