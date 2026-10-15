package sql

import (
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// Selection retains the selected row's lexical value descriptor. Only its
// ordinal and arm discriminator leave the source scopes; payloads need not be
// encoded into an untyped JSON union before an observer can recover them.
type jsonDeferredSelection struct {
	arms     []jsonDeferredArm
	fallback *jsonDeferredValue
}

func (b *builder) selectDeferredValue(n hff.Node, value *jsonDeferredValue, index *deferredAccessIndex) (*jsonDeferredValue, error) {
	plan, err := b.deferredValueList(n, value)
	if err != nil {
		return nil, err
	}
	operand, err := b.lowerDeferredIndex(index)
	if err != nil {
		return nil, err
	}
	numeric, err := b.numericOperand(index.node, operand)
	if err != nil {
		return nil, err
	}
	var positions []string
	if numeric.integerPossible {
		positions = append(positions, "WHEN "+numeric.isInteger+" THEN "+numeric.integer.code)
	}
	if numeric.floatPossible {
		f := numeric.floating.code
		valid := both(numeric.isFloat, "("+f+">=-9223372036854775808::double precision AND "+f+"<9223372036854775808::double precision AND "+f+"=trunc("+f+"))")
		// The guarded cast recovers the represented integral binary64 value.
		// Its shortest round-trip decimal spelling need not be that integer.
		positions = append(positions, "WHEN "+valid+" THEN "+f+"::bigint")
	}
	position := "NULL::bigint"
	if len(positions) > 0 {
		position = b.project("(CASE " + strings.Join(positions, " ") + " ELSE NULL::bigint END)")
	}
	var rows []string
	for i, arm := range plan.arms {
		if err := checkContext(b.ctx); err != nil {
			return nil, err
		}
		rows = append(rows, "SELECT "+strconv.Itoa(i)+" AS arm,"+deferredOrdinal(arm)+" AS ord FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
	}
	if len(rows) == 0 {
		rows = append(rows, "SELECT 0 AS arm,'{}'::bigint[] AS ord WHERE FALSE")
	}
	source, ranked, seed, picked, chosen := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	cte := "WITH " + source + " AS MATERIALIZED (" + strings.Join(rows, " UNION ALL ") + ")," + ranked + " AS (SELECT arm,ord,row_number() OVER(ORDER BY ord)-1 AS pos,count(*) OVER() AS n FROM " + source + ")"
	active := both(value.present, both("(NOT "+plan.null+")", operand.present))
	selectRow := cte + " SELECT arm,ord FROM " + ranked + " WHERE " + active + " AND pos=(CASE WHEN " + position + "<0 THEN n+" + position + " ELSE " + position + " END) ORDER BY ord LIMIT 1"
	b.steps = append(b.steps, "(SELECT "+picked+".arm,"+picked+".ord FROM (VALUES (1)) AS "+seed+"(n) LEFT JOIN LATERAL ("+selectRow+") AS "+picked+" ON TRUE OFFSET 0) AS "+chosen)
	selection := &jsonDeferredSelection{arms: append([]jsonDeferredArm(nil), plan.arms...), fallback: deferredNull()}
	for i := range selection.arms {
		arm := &selection.arms[i]
		arm.selected = both(arm.selected, "("+chosen+".arm="+strconv.Itoa(i)+" AND "+chosen+".ord="+deferredOrdinal(*arm)+")")
	}
	present := both(value.present, "("+plan.null+" OR "+operand.present+")")
	return b.finishDeferredSelection(n, selection, present)
}

func (b *builder) finishDeferredSelection(n hff.Node, selection *jsonDeferredSelection, present string) (*jsonDeferredValue, error) {
	var rows []string
	types := []hff.Type{selection.fallback.typ}
	for _, arm := range selection.arms {
		if err := checkContext(b.ctx); err != nil {
			return nil, err
		}
		rows = append(rows, "SELECT "+deferredParameterPresence(arm.value)+" AS p FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
		if !arm.empty {
			types = append(types, arm.value.typ)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "SELECT TRUE AS p WHERE FALSE")
	}
	state := b.nextAlias()
	valid := "(WITH " + state + " AS MATERIALIZED (" + strings.Join(rows, " UNION ALL ") + ") SELECT CASE WHEN EXISTS(SELECT 1 FROM " + state + ") THEN COALESCE((SELECT p FROM " + state + " LIMIT 1),FALSE) ELSE " + selection.fallback.present + " END)"
	return &jsonDeferredValue{typ: hff.NewUnionType(types...), present: both(present, b.project(valid)), selection: selection}, nil
}

func (b *builder) mapDeferredSelection(n hff.Node, value *jsonDeferredValue, apply func(*jsonDeferredValue) (*jsonDeferredValue, error)) (*jsonDeferredValue, error) {
	input := value.selection
	selection := &jsonDeferredSelection{arms: append([]jsonDeferredArm(nil), input.arms...)}
	for i, arm := range input.arms {
		var child *jsonDeferredValue
		steps, parameters, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			child, err = apply(deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return nil, err
		}
		selection.arms[i].steps, selection.arms[i].parameters = steps, parameters
		selection.arms[i].value = deferredValueParameter(child)
	}
	fallback, err := apply(input.fallback)
	if err != nil {
		return nil, err
	}
	selection.fallback = fallback
	return b.finishDeferredSelection(n, selection, value.present)
}

// Resolve an observation inside each selected row, then export only a common
// scalar result. SQL NULL payloads are distinct from an absent selected row.
func (b *builder) resolveDeferredSelection(n hff.Node, value *jsonDeferredValue, kind hff.TypeKind, apply func(*jsonDeferredValue) (expression, error)) (expression, error) {
	var rows []string
	for _, arm := range value.selection.arms {
		var result expression
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			result, err = apply(deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return expression{}, err
		}
		arm.steps = steps
		rows = append(rows, "SELECT "+result.code+" AS v,"+result.present+" AS p FROM "+deferredFrom(arm)+" WHERE "+arm.selected)
	}
	fallback, err := apply(value.selection.fallback)
	if err != nil {
		return expression{}, err
	}
	if len(rows) == 0 {
		rows = append(rows, "SELECT "+fallback.code+" AS v,TRUE AS p WHERE FALSE")
	}
	rowsName, output := b.nextAlias(), b.nextAlias()
	matched := "EXISTS(SELECT 1 FROM " + rowsName + ")"
	code := "(CASE WHEN " + matched + " THEN (SELECT v FROM " + rowsName + " LIMIT 1) ELSE " + fallback.code + " END)"
	present := both(value.present, "(CASE WHEN "+matched+" THEN COALESCE((SELECT p FROM "+rowsName+" LIMIT 1),FALSE) ELSE "+fallback.present+" END)")
	b.steps = append(b.steps, "(WITH "+rowsName+" AS MATERIALIZED ("+strings.Join(rows, " UNION ALL ")+") SELECT "+code+" AS v,"+present+" AS p OFFSET 0) AS "+output)
	return expression{output + ".v", output + ".p", kind}, nil
}

func (b *builder) observeDeferredSelection(n hff.Node, value *jsonDeferredValue, name string) (expression, error) {
	kind := hff.StringType
	if name == "truth" || name == "exists" {
		kind = hff.BoolType
	} else if name == "size" || name == "count" {
		kind = hff.IntType
	}
	result, err := b.resolveDeferredSelection(n, value, kind, func(child *jsonDeferredValue) (expression, error) {
		return b.observeDeferredValue(n, child, name)
	})
	if err == nil && kind == hff.BoolType {
		result = boolean(both(result.present, "COALESCE("+result.code+",FALSE)"))
	}
	return result, err
}

func (b *builder) materializeDeferredSelection(n hff.Node, value *jsonDeferredValue) (expression, error) {
	types := []hff.Type{value.selection.fallback.typ}
	for _, arm := range value.selection.arms {
		if err := checkContext(b.ctx); err != nil {
			return expression{}, err
		}
		if arm.empty {
			continue
		}
		for _, previous := range types {
			if jsonTaggedStringStateAmbiguous(previous, arm.value.typ) {
				return expression{}, b.failure(n, "selected row alternatives require recoverable value types")
			}
		}
		types = append(types, arm.value.typ)
	}
	typ := hff.NewUnionType(types...)
	if err := b.checkJSONStringSchemas(n, typ); err != nil {
		return expression{}, err
	}
	result, err := b.resolveDeferredSelection(n, value, hff.AnyType, func(child *jsonDeferredValue) (expression, error) {
		carrier, err := b.materializeDeferredValue(n, child)
		if err != nil {
			return expression{}, err
		}
		raw, err := b.computedJSONDocument(n, carrier, child.typ)
		return expression{raw, carrier.present, hff.AnyType}, err
	})
	if err != nil {
		return expression{}, err
	}
	b.jsonValues[result.code] = JSON
	decodeType := typ
	if typ.Kind() == hff.NullType && n.Type().Kind() != hff.NeverType {
		// A private proof can rule out every non-null alternative even when
		// the public expression still has a typed result. Retain its ordinary
		// SQL carrier so surrounding calls/comparisons keep their operands.
		decodeType = n.Type()
	}
	return b.jsonDecode(n, result.code, JSON, decodeType, result.present)
}

func (b *builder) deferredSelectionList(n hff.Node, value *jsonDeferredValue) (jsonDeferredList, error) {
	var arms []jsonDeferredArm
	state, err := b.resolveDeferredSelection(n, value, hff.BoolType, func(child *jsonDeferredValue) (expression, error) {
		plan, err := b.deferredValueList(n, child)
		if err != nil {
			return expression{}, err
		}
		return expression{plan.null, plan.present, hff.BoolType}, nil
	})
	if err != nil {
		return jsonDeferredList{}, err
	}
	for _, arm := range value.selection.arms {
		var child jsonDeferredList
		steps, parameters, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			child, err = b.deferredValueList(n, deferredLeaf(arm.value))
			return err
		})
		if err != nil {
			return jsonDeferredList{}, err
		}
		arm.steps, arm.parameters = steps, parameters
		for _, nested := range child.arms {
			if err := checkContext(b.ctx); err != nil {
				return jsonDeferredList{}, err
			}
			nested.from = deferredFrom(arm) + " CROSS JOIN LATERAL " + nested.from
			nested.selected = both(arm.selected, nested.selected)
			nested.empty = arm.empty || nested.empty
			arms = append(arms, nested)
		}
	}
	// The missing-row fallback is ordinary null and cannot contribute rows;
	// subsequent mapped accesses/guards preserve that null or no-value state.
	return jsonDeferredList{null: state.code, present: state.present, arms: arms}, nil
}
