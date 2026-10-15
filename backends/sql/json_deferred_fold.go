package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// A deferred input need not be representable as an ordinary JSON list. Cache
// typed packets once, then interpret the chosen input descriptor in each fold
// iteration. The accumulator still uses the established recoverable JSON
// invariant; this does not erase ambiguous accumulator alternatives into Any.
func (b *builder) jsonDeferredInputFold(n hff.Node, name string) (expression, bool, error) {
	receiver, _ := n.Child(0)
	if b.jsonItemsRoles(receiver) != nil {
		return expression{}, false, nil
	}
	needed, err := b.needsDeferredFlatmap(receiver)
	if err != nil {
		return expression{}, true, err
	}
	source, stages := mapPipelineStages(receiver)
	if !needed && !b.mapPipelineHasItemSlot(source, stages) {
		return expression{}, false, nil
	}
	// Prove identity before trying the ordinary carrier. Successful lowering
	// alone is insufficient: Any can absorb a per-key tagged alternative.
	prepared := b.validationBuilder()
	plan, err := prepared.deferredList(receiver)
	if err != nil {
		return expression{}, true, err
	}
	safe, err := prepared.deferredInputIdentity(&jsonDeferredValue{typ: b.jsonStoredType(receiver), present: plan.present, list: &plan})
	if err != nil {
		return expression{}, true, err
	}
	if safe {
		probe := b.validationBuilder()
		if result, err := probe.jsonOrdinaryFold(n, name); err == nil {
			memo := b.itemRoleMemo
			*b = probe
			b.itemRoleMemo = memo
			return result, true, nil
		}
		if err := checkContext(b.ctx); err != nil {
			return expression{}, true, err
		}
	}
	memo := b.itemRoleMemo
	*b = prepared
	b.itemRoleMemo = memo
	if len(plan.arms) == 0 {
		// An always-empty/null stream still validates its callback, without
		// introducing a runtime row or changing its null/presence state.
		alias := b.nextAlias()
		null := expression{"NULL", "TRUE", hff.NullType}
		plan.arms = []jsonDeferredArm{{empty: true, from: "(SELECT 1 WHERE FALSE) AS " + alias,
			ordinal: []string{"0"}, selected: "TRUE", value: jsonParameter{typ: hff.PrimitiveType(hff.NeverType), direct: &null}, parameters: copyJSONParameters(b.parameters)}}
	}
	var packets []jsonDeferredPacket
	var rows []string
	for i, arm := range plan.arms {
		var packet jsonDeferredPacket
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			packet, err = b.packDeferredValue(n, deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return expression{}, true, err
		}
		arm.steps = steps
		rows = append(rows, "SELECT "+strconv.Itoa(i)+" AS arm,"+deferredOrdinal(arm)+" AS ord,"+packet.code+" AS value FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
		packets = append(packets, packet)
	}
	if len(rows) == 0 {
		rows = append(rows, "SELECT 0 AS arm,'{}'::bigint[] AS ord,'null'::json AS value WHERE FALSE")
	}
	seed, _ := n.Child(1)
	initial, err := b.expression(seed)
	if err != nil {
		return expression{}, true, err
	}
	seedType := b.jsonStoredType(seed)
	rawSeed, err := b.computedJSONDocument(seed, initial, seedType)
	if err != nil {
		return expression{}, true, err
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return expression{}, true, b.failure(n, "fold requires two callback parameters")
	}
	acc := 0
	if name == "foldr" {
		acc = 1
	}
	accumulatorType, err := b.jsonFoldType(n, parameters[acc].Type())
	if err != nil {
		return expression{}, true, err
	}
	proof := b.jsonFoldItemProvenance(n)
	items, state, previous, item, result, final, inputs := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	var successors []string
	for i, packet := range packets {
		gate := b.nextAlias()
		value, steps, _, err := b.jsonFoldBodyInput(callback, parameters, acc, previous, item, accumulatorType, seedType, receiver, seed, JSON, proof, func() (jsonParameter, error) {
			descriptor, err := packet.decode(b, n, gate+".value")
			if err != nil {
				return jsonParameter{}, err
			}
			return deferredValueParameter(descriptor), nil
		})
		if err != nil {
			return expression{}, true, err
		}
		raw := value.code
		from := "(SELECT " + item + ".value WHERE " + item + ".arm=" + strconv.Itoa(i) + " OFFSET 0) AS " + gate
		if len(steps) > 0 {
			from += " CROSS JOIN LATERAL " + strings.Join(steps, " CROSS JOIN LATERAL ")
		}
		successors = append(successors, "SELECT "+raw+" AS v,"+value.present+" AS p FROM "+from)
	}
	if len(successors) == 0 {
		successors = append(successors, "SELECT 'null'::json AS v,TRUE AS p WHERE FALSE")
	}
	order := "ASC"
	if name == "foldr" {
		order = "DESC"
	}
	cte := "WITH RECURSIVE " + items + " AS MATERIALIZED (SELECT arm,value,row_number() OVER(ORDER BY ord " + order + ") AS pos FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + inputs + ")," + state + "(pos,acc,p) AS (SELECT 0::bigint," + rawSeed + "::json," + initial.present + " UNION ALL SELECT " + previous + ".pos+1," + result + ".v," + result + ".p FROM " + state + " AS " + previous + " JOIN " + items + " AS " + item + " ON " + item + ".pos=" + previous + ".pos+1 CROSS JOIN LATERAL (" + strings.Join(successors, " UNION ALL ") + ") AS " + result + " WHERE " + previous + ".p)"
	b.steps = append(b.steps, "("+cte+" SELECT acc,p FROM "+state+" ORDER BY pos DESC LIMIT 1 OFFSET 0) AS "+final)
	code := "(CASE WHEN " + plan.null + " THEN NULL ELSE " + final + ".acc END)"
	present := both(plan.present, "("+plan.null+" OR "+final+".p)")
	resultType, err := b.jsonFoldType(n, n.Type())
	if err != nil {
		return expression{}, true, err
	}
	value, err := b.jsonDecode(n, code, JSON, resultType, present)
	return value, true, err
}
