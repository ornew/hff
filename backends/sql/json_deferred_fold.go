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
	prune, err := b.unusedDeferredFoldInput(n, name)
	if err != nil {
		return expression{}, true, err
	}
	var fields map[string]bool
	var nativeFields map[string]bool
	if !prune {
		fields, err = b.observedDeferredFoldFields(n, name)
		if err != nil {
			return expression{}, true, err
		}
		nativeFields = fields
		if fields != nil {
			eligible, err := b.hasUnobservedDeferredFoldFields(plan, fields)
			if err != nil {
				return expression{}, true, err
			}
			if !eligible {
				fields = nil
			}
		}
	}
	var indexes []float64
	if !prune && fields == nil {
		indexes, err = b.observedDeferredFoldIndexes(n, name)
		if err != nil {
			return expression{}, true, err
		}
		if indexes != nil {
			fixed, err := b.hasFixedDeferredFoldInputs(plan)
			if err != nil {
				return expression{}, true, err
			}
			if !fixed {
				indexes = nil
			}
		}
	}
	projections, err := b.nativeDeferredFoldProjections(plan, nativeFields, indexes)
	if err != nil {
		return expression{}, true, err
	}
	native := false
	if !prune && fields == nil && indexes == nil && projections == nil {
		native, err = b.nativeScalarDeferredFoldInputs(n, plan)
		if err != nil {
			return expression{}, true, err
		}
	}
	if prune || fields != nil || indexes != nil || native || projections != nil {
		// Validate the complete original packet, seed and callback path in
		// receiver-first order before omitting an unobserved payload. Keep
		// nested folds on their original validation path as well.
		probe := b.validationBuilder()
		probe.disableDeferredFoldPruning = true
		if _, _, err := probe.jsonDeferredFoldPlan(n, name, plan, false, nil, nil, false, nil); err != nil {
			return expression{}, true, err
		}
	}
	return b.jsonDeferredFoldPlan(n, name, plan, prune, fields, indexes, native, projections)
}

func (b *builder) jsonDeferredFoldPlan(n hff.Node, name string, plan jsonDeferredList, prune bool, fields map[string]bool, indexes []float64, native bool, projections []*jsonDeferredFoldProjection) (expression, bool, error) {
	receiver, _ := n.Child(0)
	if len(plan.arms) == 0 {
		// An always-empty/null stream still validates its callback, without
		// introducing a runtime row or changing its null/presence state.
		alias := b.nextAlias()
		null := expression{"NULL", "TRUE", hff.NullType}
		plan.arms = []jsonDeferredArm{{empty: true, from: "(SELECT 1 WHERE FALSE) AS " + alias,
			ordinal: []string{"0"}, selected: "TRUE", value: jsonParameter{typ: hff.PrimitiveType(hff.NeverType), direct: &null}, parameters: copyJSONParameters(b.parameters)}}
	}
	var layout []jsonDeferredFoldColumn
	if native {
		var err error
		layout, err = b.nativeDeferredFoldColumns(plan)
		if err != nil {
			return expression{}, true, err
		}
	}
	if projections != nil {
		var err error
		layout, err = b.projectedDeferredFoldColumns(projections)
		if err != nil {
			return expression{}, true, err
		}
	}
	var transports []jsonDeferredFoldTransport
	var empty []bool
	var rows []string
	for i, arm := range plan.arms {
		var transport jsonDeferredFoldTransport
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			if projections != nil && !arm.empty {
				transport.projection = projections[i]
				return nil
			}
			if native && !arm.empty {
				parameter, _ := b.nativeScalarDeferredFoldParameter(arm.value)
				transport.native = &parameter
				return nil
			}
			var packet jsonDeferredPacket
			var err error
			if prune {
				packet = unusedDeferredFoldPacket(arm.value.typ)
			} else if value := deferredLeaf(arm.value); fields != nil && value.object != nil {
				packet, err = b.packDeferredObject(n, value, fields)
			} else if indexes != nil {
				positions, fixed, failure := b.fixedDeferredFoldPositions(value)
				if failure != nil {
					return failure
				}
				if fixed {
					packet, err = b.packFixedDeferredFoldList(n, value, positions, indexes)
				} else {
					packet, err = b.packDeferredValue(n, value)
				}
			} else {
				packet, err = b.packDeferredValue(n, value)
			}
			transport.packet = packet
			return err
		})
		if err != nil {
			return expression{}, true, err
		}
		arm.steps = steps
		if !arm.empty {
			rows = append(rows, "SELECT "+strconv.Itoa(i)+" AS arm,"+deferredOrdinal(arm)+" AS ord,"+transport.columns(layout)+" FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
		}
		transports = append(transports, transport)
		empty = append(empty, arm.empty)
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
	for i, transport := range transports {
		gate := b.nextAlias()
		var value expression
		var steps []string
		err := b.validateEmptyDeferredWork(empty[i], func() error {
			var err error
			value, steps, _, err = b.jsonFoldBodyInput(callback, parameters, acc, previous, item, accumulatorType, seedType, receiver, seed, JSON, proof, func() (jsonParameter, error) {
				return transport.decodeInput(b, n, gate)
			})
			return err
		})
		if err != nil {
			return expression{}, true, err
		}
		if empty[i] {
			continue
		}
		raw := value.code
		column := transport.gateColumns(item)
		from := "(SELECT " + column + " WHERE " + item + ".arm=" + strconv.Itoa(i) + " OFFSET 0) AS " + gate
		if len(steps) > 0 {
			from += " CROSS JOIN LATERAL " + strings.Join(steps, " CROSS JOIN LATERAL ")
		}
		successors = append(successors, "SELECT "+raw+" AS v,"+value.present+" AS p FROM "+from)
	}
	if len(rows) == 0 {
		resultType, err := b.jsonFoldType(n, n.Type())
		if err != nil {
			return expression{}, true, err
		}
		code := "(CASE WHEN " + plan.null + " THEN NULL ELSE " + rawSeed + " END)"
		present := both(plan.present, "("+plan.null+" OR "+initial.present+")")
		value, err := b.jsonDecode(n, code, JSON, resultType, present)
		return value, true, err
	}
	order := "ASC"
	if name == "foldr" {
		order = "DESC"
	}
	columns := "arm,value"
	if native || projections != nil {
		columns = "arm"
		if projections != nil {
			columns += ",input_null"
		}
		for _, column := range layout {
			columns += "," + column.name
		}
	}
	cte := "WITH RECURSIVE " + items + " AS MATERIALIZED (SELECT " + columns + ",row_number() OVER(ORDER BY ord " + order + ") AS pos FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + inputs + ")," + state + "(pos,acc,p) AS (SELECT 0::bigint," + rawSeed + "::json," + initial.present + " UNION ALL SELECT " + previous + ".pos+1," + result + ".v," + result + ".p FROM " + state + " AS " + previous + " JOIN " + items + " AS " + item + " ON " + item + ".pos=" + previous + ".pos+1 CROSS JOIN LATERAL (" + strings.Join(successors, " UNION ALL ") + ") AS " + result + " WHERE " + previous + ".p)"
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
