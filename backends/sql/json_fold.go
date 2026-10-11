package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Recursive SQL carries the accumulator as JSON and no-value as a separate
// boolean. A null accumulator is data and continues to the next callback.
func (b *builder) jsonFold(n hff.Node, name string) (expression, error) {
	receiver, _ := n.Child(0)
	value, err := b.expression(receiver)
	if err != nil {
		return expression{}, err
	}
	if value.kind == hff.NullType {
		return value, nil
	}
	format, ok := b.jsonValues[value.code]
	if !ok || value.kind != hff.ListType {
		return expression{}, b.failure(n, "fold requires a JSON list receiver")
	}
	seed, _ := n.Child(1)
	initial, err := b.expression(seed)
	if err != nil {
		return expression{}, err
	}
	rawSeed, _, err := b.jsonRepresentation(seed, initial)
	if err != nil {
		return expression{}, err
	}
	callback, _ := n.Child(2)
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 2 {
		return expression{}, b.failure(n, "fold requires two callback parameters")
	}
	items, state, previous, item, final, source := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	accumulatorIndex := 0
	if name == "foldr" {
		accumulatorIndex = 1
	}
	accumulatorType, err := b.jsonFoldType(n, parameters[accumulatorIndex].Type())
	if err != nil {
		return expression{}, err
	}
	result, steps, err := b.jsonFoldBody(callback, parameters, accumulatorIndex, previous, item, accumulatorType)
	if err != nil {
		return expression{}, err
	}
	rawResult, _, err := b.jsonRepresentation(n, result)
	if err != nil {
		return expression{}, err
	}
	ordinal := previous + ".pos+1"
	if name == "foldr" {
		ordinal = string(format) + "_array_length(" + value.code + ")::bigint-" + previous + ".pos"
	}
	from := state + " AS " + previous + " JOIN " + items + " AS " + item + " ON " + item + ".ord=" + ordinal
	if len(steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(steps, " CROSS JOIN LATERAL ")
	}
	cte := "WITH RECURSIVE " + items + " AS MATERIALIZED (SELECT value::json AS value,ord FROM " + string(format) + "_array_elements(" + value.code + ") WITH ORDINALITY AS " + source + "(value,ord)), " + state + "(pos,acc,p) AS (SELECT 0::bigint," + rawSeed + "::json," + initial.present + " UNION ALL SELECT " + previous + ".pos+1," + rawResult + "::json," + result.present + " FROM " + from + " WHERE " + previous + ".p)"
	// The last reached state is either the final accumulator or the first
	// no-value failure. An empty list leaves the seed state intact.
	b.steps = append(b.steps, "("+cte+" SELECT acc,p FROM "+state+" ORDER BY pos DESC LIMIT 1 OFFSET 0) AS "+final)
	code := "(CASE WHEN " + value.code + " IS NULL THEN NULL ELSE " + final + ".acc END)"
	present := both(value.present, "("+value.code+" IS NULL OR "+final+".p)")
	resultType, err := b.jsonFoldType(n, n.Type())
	if err != nil {
		return expression{}, err
	}
	return b.jsonDecode(n, code, JSON, resultType, present)
}

// Lists with differing element shapes have one container representation. Merge
// those shapes for decoding only; the checked IR retains the exact union type.
func (b *builder) jsonFoldType(n hff.Node, typ hff.Type) (hff.Type, error) {
	members, err := b.jsonEqualityMembers(n, typ)
	if err != nil {
		return hff.Type{}, err
	}
	if len(members) == 0 {
		return hff.PrimitiveType(hff.NullType), nil
	}
	if len(members) != 1 {
		return hff.Type{}, b.failure(n, "fold accumulator requires one JSON-distinguishable concrete type")
	}
	return hff.NewUnionType(members[0], hff.PrimitiveType(hff.NullType)), nil
}

func (b *builder) jsonFoldBody(callback hff.Node, parameters []hff.Binding, accumulatorIndex int, previous, item string, accumulatorType hff.Type) (expression, []string, error) {
	outer := b.steps
	outerPaths := b.sharedPaths
	b.steps = nil
	b.sharedPaths = nil
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	old := make([]jsonParameter, len(parameters))
	existed := make([]bool, len(parameters))
	for i, binding := range parameters {
		id := binding.ID()
		old[i], existed[i] = b.parameters[id]
		parameter := jsonParameter{code: item + ".value", format: JSON}
		if i == accumulatorIndex {
			parameter.code, parameter.typ = previous+".acc", accumulatorType
		}
		b.parameters[id] = parameter
	}
	defer func() {
		b.steps = outer
		b.sharedPaths = outerPaths
		for i, binding := range parameters {
			if existed[i] {
				b.parameters[binding.ID()] = old[i]
			} else {
				delete(b.parameters, binding.ID())
			}
		}
	}()
	body, _ := callback.Child(0)
	result, err := b.expression(body)
	return result, b.steps, err
}
