package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) runtimeFoldDescriptor(n hff.Node, name string) (*jsonDeferredValue, error) {
	// Preserve the established SQL for ordinary and closed positional state.
	// The probe owns all aliases, arguments and scope maps until it succeeds.
	probe := b.validationBuilder()
	probe.runtimeFoldProbe = true
	if result, err := probe.jsonFold(n, name); err == nil {
		memo, probing := b.itemRoleMemo, b.runtimeFoldProbe
		*b = probe
		b.itemRoleMemo, b.runtimeFoldProbe = memo, probing
		return deferredLeaf(jsonParameter{typ: b.jsonStoredType(n), direct: &result, format: b.jsonValues[result.code],
			lossy: b.losesFloat(n, nil), itemRoles: b.jsonItemRoles(n), itemListRoles: b.jsonItemsRoles(n)}), nil
	}
	if err := checkContext(b.ctx); err != nil {
		return nil, err
	}
	receiver, _ := n.Child(0)
	plan, err := b.deferredList(receiver)
	if err != nil {
		return nil, err
	}
	var rows []string
	receiverLoss := b.losesFloat(receiver, nil)
	for _, arm := range plan.arms {
		receiverLoss = receiverLoss || !arm.empty && deferredValueLoss(deferredLeaf(arm.value))
		var code string
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			code, err = b.encodeRuntimeValue(n, deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return nil, err
		}
		arm.steps = steps
		if arm.empty {
			continue
		}
		rows = append(rows, "SELECT "+deferredOrdinal(arm)+" AS ord,"+code+" AS value FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
	}
	seed, _ := n.Child(1)
	initial, err := b.deferredValue(seed)
	if err != nil {
		return nil, err
	}
	rawSeed, err := b.encodeRuntimeValue(seed, deferredLeaf(initial))
	if err != nil {
		return nil, err
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return nil, b.failure(n, "fold requires two callback parameters")
	}
	items, state, previous, item, final, source := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	acc := 0
	order := "ASC"
	if name == "foldr" {
		acc, order = 1, "DESC"
	}
	provenance, err := b.runtimeItemProvenance(n)
	if err != nil {
		return nil, err
	}
	var result expression
	var steps []string
	var lossy bool
	err = b.validateEmptyDeferredWork(len(rows) == 0, func() error {
		var err error
		result, steps, lossy, err = b.runtimeFoldBody(callback, parameters, acc, previous+".acc", item+".value", n.Type(), receiver, seed, receiverLoss, deferredValueLoss(deferredLeaf(initial)), provenance)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		code := "(CASE WHEN " + plan.null + " THEN " + runtimeNull + " ELSE " + rawSeed + " END)"
		present := both(plan.present, "("+plan.null+" OR "+deferredParameterPresence(initial)+")")
		value := runtimeValue(code, n.Type(), present, deferredValueLoss(deferredLeaf(initial)))
		value.runtime.itemRoles, value.runtime.itemListRoles = provenance.pair, provenance.list
		return value, nil
	}
	from := state + " AS " + previous + " JOIN " + items + " AS " + item + " ON " + item + ".pos=" + previous + ".pos+1"
	if len(steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(steps, " CROSS JOIN LATERAL ")
	}
	cte := "WITH RECURSIVE " + items + " AS MATERIALIZED (SELECT value,row_number() OVER(ORDER BY ord " + order + ") AS pos FROM (" + strings.Join(rows, " UNION ALL ") + ") AS " + source + ")," + state + "(pos,acc,p) AS (SELECT 0::bigint," + rawSeed + "," + deferredParameterPresence(initial) + " UNION ALL SELECT " + previous + ".pos+1," + result.code + "," + result.present + " FROM " + from + " WHERE " + previous + ".p)"
	b.steps = append(b.steps, "("+cte+" SELECT acc,p FROM "+state+" ORDER BY pos DESC LIMIT 1 OFFSET 0) AS "+final)
	code := "(CASE WHEN " + plan.null + " THEN " + runtimeNull + " ELSE " + final + ".acc END)"
	present := both(plan.present, "("+plan.null+" OR "+final+".p)")
	value := runtimeValue(code, n.Type(), present, lossy)
	value.runtime.itemRoles, value.runtime.itemListRoles = provenance.pair, provenance.list
	return value, nil
}

func (b *builder) runtimeFoldBody(callback hff.Node, parameters []hff.Binding, acc int, previous, item string, typ hff.Type, receiver, seed hff.Node, receiverLoss, seedLoss bool, provenance jsonItemProvenance) (expression, []string, bool, error) {
	outerSteps, outerPaths, outerParameters, outerMemo := b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo
	b.steps, b.sharedPaths, b.itemRoleMemo = nil, nil, nil
	b.parameters = copyJSONParameters(outerParameters)
	defer func() {
		b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo = outerSteps, outerPaths, outerParameters, outerMemo
	}()
	body, _ := callback.Child(0)
	loss := seedLoss || receiverLoss && deferredTypeMayFloat(typ)
	environment := map[hff.BindingID]bool{}
	for i, parameter := range parameters {
		environment[parameter.ID()] = receiverLoss
		if i == acc {
			environment[parameter.ID()] = loss
		}
	}
	for i := 0; i < 2; i++ {
		loss = loss || b.losesFloat(body, environment)
		environment[parameters[acc].ID()] = loss
	}
	itemProvenance, err := b.runtimeItemProvenance(receiver)
	if err != nil {
		return expression{}, nil, false, err
	}
	for i, parameter := range parameters {
		value := runtimeValue(item, jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true), "TRUE", receiverLoss)
		value.runtime.itemRoles = itemProvenance.list
		if i == acc {
			value = runtimeValue(previous, typ, "TRUE", loss)
			value.runtime.itemRoles, value.runtime.itemListRoles = provenance.pair, provenance.list
		}
		b.parameters[parameter.ID()] = deferredValueParameter(value)
	}
	value, err := b.deferredValue(body)
	if err != nil {
		return expression{}, nil, false, err
	}
	code, err := b.encodeRuntimeValue(body, deferredLeaf(value))
	return expression{code, deferredParameterPresence(value), hff.AnyType}, append([]string(nil), b.steps...), loss, err
}
