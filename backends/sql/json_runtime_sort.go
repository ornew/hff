package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) runtimeSortDescriptor(n hff.Node) (*jsonDeferredValue, error) {
	// Successful ordinary lowering alone cannot prove value identity: Any
	// may have absorbed tagged alternatives at the same structural position.
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	receiver, _ := n.Child(0)
	plan, err := probe.deferredList(receiver)
	if err != nil {
		return nil, err
	}
	value := &jsonDeferredValue{typ: probe.jsonStoredType(receiver), present: plan.present, list: &plan}
	safe, err := probe.deferredInputIdentity(value)
	if err != nil {
		return nil, err
	}
	if safe {
		normal := b.validationBuilder()
		normal.keepDeferredValues = false
		result, err := normal.call(n)
		if err == nil {
			b.adoptDeferredProbe(normal)
			return deferredLeaf(jsonParameter{typ: b.jsonStoredType(n), direct: &result, format: b.jsonValues[result.code],
				lossy: b.losesFloat(n, nil), itemRoles: b.jsonItemRoles(n), itemListRoles: b.jsonItemsRoles(n)}), nil
		}
		if err := b.typedFallbackError(err); err != nil {
			return nil, err
		}
	}
	code, err := probe.encodeRuntimeValue(receiver, value)
	if err != nil {
		return nil, err
	}
	code = probe.project(code)
	rows, source := probe.nextAlias(), probe.nextAlias()
	item := source + ".value"
	tag, text := "("+item+"->>0)", "("+item+"->1 #>> '{}')"
	family := "(CASE WHEN " + tag + " IN ('int','float') THEN 'number' ELSE " + tag + " END)"
	stored := sortListType(value.typ)
	payload := "(CASE WHEN " + code + "->>0='list' THEN " + code + "->1 ELSE '[]'::json END)"
	from := "json_array_elements(" + payload + ") WITH ORDINALITY AS " + source + "(value,ord)"
	var keys []string
	for _, kind := range []hff.TypeKind{hff.IntType, hff.FloatType, hff.DurationType, hff.TimestampType} {
		if err := checkContext(probe.ctx); err != nil {
			return nil, err
		}
		if !runtimeSortMayContain(stored.Element(), kind) {
			continue
		}
		var key string
		switch kind {
		case hff.IntType:
			key = text + "::bigint::numeric"
		case hff.FloatType:
			decoded := probe.nextAlias()
			from += " CROSS JOIN LATERAL (SELECT CASE WHEN " + tag + "='float' THEN " + jsonFloatCast(text) + " END AS value OFFSET 0) AS " + decoded
			key = numericOrderKey(decoded + ".value")
		case hff.DurationType:
			key = probe.parseDurationInline(text)
		case hff.TimestampType:
			key = jsonTimestampNanoseconds(text)
		}
		keys = append(keys, "WHEN "+tag+"='"+runtimeTag(kind)+"' THEN "+key)
	}
	numeric := "NULL::numeric"
	if len(keys) > 0 {
		numeric = "(CASE " + strings.Join(keys, " ") + " ELSE NULL::numeric END)"
	}
	stringKey := "(CASE WHEN " + tag + "='string' THEN " + text + " END) COLLATE \"C\""
	cte := "WITH " + rows + " AS MATERIALIZED (SELECT " + item + " AS value," + source + ".ord," + family + " AS family," + numeric + " AS number," + stringKey + " AS text FROM " + from + ")"
	invalid := "EXISTS(SELECT 1 FROM " + rows + " WHERE family NOT IN ('number','string','duration','timestamp') OR (family<>'string' AND number IS NULL))"
	mixed := "(SELECT count(DISTINCT family)>1 FROM " + rows + ")"
	// Keep the original node and its ordinal. Numeric ties must not replace
	// an Int with Float, change signed zero, or reorder equal instants.
	aggregate := "(SELECT COALESCE(json_agg(value ORDER BY number,text,ord),'[]'::json) FROM " + rows + ")"
	output := "(CASE WHEN " + code + "->>0='list' THEN (" + cte + " SELECT CASE WHEN " + invalid + " OR " + mixed + " THEN " + runtimeNull + " ELSE " + runtimeNode("list", aggregate) + " END) ELSE " + runtimeNull + " END)"
	resultValue := runtimeValue(probe.project(output), n.Type(), value.present, deferredValueLoss(value))
	b.adoptDeferredProbe(probe)
	return resultValue, nil
}

func runtimeSortMayContain(typ hff.Type, kind hff.TypeKind) bool {
	if typ.Kind() == hff.AnyType || typ.Kind() == kind {
		return true
	}
	for _, member := range typ.Members() {
		if runtimeSortMayContain(member, kind) {
			return true
		}
	}
	return false
}
