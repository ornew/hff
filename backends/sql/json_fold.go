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
	value, format, err := b.jsonCallbackReceiver(n, value)
	if err != nil {
		return expression{}, err
	}
	seed, _ := n.Child(1)
	initial, err := b.expression(seed)
	if err != nil {
		return expression{}, err
	}
	rawSeed, seedFormat, err := b.jsonRepresentation(seed, initial)
	if err != nil {
		return expression{}, err
	}
	rawSeed, err = b.comparisonDocument(seed, rawSeed, seedFormat, b.jsonStoredType(seed))
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
	result, steps, storedType, err := b.jsonFoldBody(callback, parameters, accumulatorIndex, previous, item, accumulatorType, receiver, seed, format)
	if err != nil {
		return expression{}, err
	}
	rawResult, resultFormat, err := b.jsonRepresentation(n, result)
	if err != nil {
		return expression{}, err
	}
	body, _ := callback.Child(0)
	rawResult, err = b.comparisonDocument(body, rawResult, resultFormat, storedType)
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
	cte := "WITH RECURSIVE " + items + " AS MATERIALIZED (SELECT value,ord FROM " + string(format) + "_array_elements(" + value.code + ") WITH ORDINALITY AS " + source + "(value,ord)), " + state + "(pos,acc,p) AS (SELECT 0::bigint," + rawSeed + "::json," + initial.present + " UNION ALL SELECT " + previous + ".pos+1," + rawResult + "::json," + result.present + " FROM " + from + " WHERE " + previous + ".p)"
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

// Recursive state uses normalized JSON, retaining the exact inferred union.
// Every seed and callback result must have a recoverable native representation.
func (b *builder) jsonFoldType(n hff.Node, typ hff.Type) (hff.Type, error) {
	// Actual seeds and results are checked before entering recursive state.
	// Removing null from Any can expand its static type to every HFF family;
	// this backend's JSON-native storage contract still bounds runtime values.
	seed, _ := n.Child(1)
	callback, _ := n.Child(2)
	parameters, _ := callback.Parameters()
	method, _ := n.Method()
	acc := 0
	if method.Name() == "foldr" {
		acc = 1
	}
	receiver, _ := n.Child(0)
	seedType := b.jsonStoredType(seed)
	itemType := jsonStoredMemberType(b.jsonStoredType(receiver), "", false, true)
	accumulator := seedType
	for attempt := 0; attempt < 64; attempt++ {
		types := make([]hff.Type, len(parameters))
		for i := range types {
			types[i] = itemType
			if i == acc {
				types[i] = accumulator
			}
		}
		bodyType := b.jsonCallbackStoredType(callback, types)
		if jsonTaggedStringStateAmbiguous(seedType, bodyType) {
			return hff.Type{}, b.failure(n, "JSON fold state cannot distinguish String, Bytes, and Duration")
		}
		stored := hff.NewUnionType(seedType, bodyType)
		carrier := jsonNativeCarrierType(typ, stored)
		if !jsonHasTaggedString(stored) || carrier.Key() == accumulator.Key() {
			return carrier, nil
		}
		accumulator = carrier
	}
	return hff.Type{}, b.failure(n, "JSON fold tagged-string storage type does not reach a finite fixed point")
}

func (b *builder) jsonFoldBody(callback hff.Node, parameters []hff.Binding, accumulatorIndex int, previous, item string, accumulatorType hff.Type, receiver, seed hff.Node, format JSONFormat) (expression, []string, hff.Type, error) {
	outer := b.steps
	outerPaths := b.sharedPaths
	b.steps = nil
	b.sharedPaths = nil
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	receiverLoss, seedLoss := b.losesFloat(receiver, nil), b.losesFloat(seed, nil)
	var provenanceRestores []func()
	defer func() {
		for i := len(provenanceRestores) - 1; i >= 0; i-- {
			provenanceRestores[i]()
		}
	}()
	for i, binding := range parameters {
		if i != accumulatorIndex {
			provenanceRestores = append(provenanceRestores, b.callbackProvenance(binding.ID(), receiver))
		}
	}
	body, _ := callback.Child(0)
	accumulatorLoss := seedLoss
	lossEnvironment := map[hff.BindingID]bool{}
	for i, binding := range parameters {
		lossEnvironment[binding.ID()] = receiverLoss
		if i == accumulatorIndex {
			lossEnvironment[binding.ID()] = accumulatorLoss
		}
	}
	// Reach the Boolean provenance fixed point without inheriting loss from
	// an unused element. Accumulator state can only gain a possible loss.
	for attempt := 0; attempt < 2; attempt++ {
		next := seedLoss || b.losesFloat(body, lossEnvironment)
		if next == accumulatorLoss {
			break
		}
		accumulatorLoss = next
		lossEnvironment[parameters[accumulatorIndex].ID()] = next
	}
	old := make([]jsonParameter, len(parameters))
	existed := make([]bool, len(parameters))
	for i, binding := range parameters {
		id := binding.ID()
		old[i], existed[i] = b.parameters[id]
		parameter := jsonParameter{code: item + ".value", format: format, lossy: receiverLoss}

		if i == accumulatorIndex {
			parameter.code, parameter.typ, parameter.format = previous+".acc", accumulatorType, JSON
			parameter.lossy = accumulatorLoss
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
	result, err := b.expression(body)
	return result, b.steps, b.jsonStoredType(body), err
}

// Compare positions before forming a union: Any can erase an explicit Bytes
// or Duration alternative during type canonicalization, although JSON cannot
// recover that typed value from an untagged string.
func jsonTaggedStringStateAmbiguous(left, right hff.Type) bool {
	if left.Kind() == hff.UnionType {
		for _, t := range left.Members() {
			if jsonTaggedStringStateAmbiguous(t, right) {
				return true
			}
		}
		return false
	}
	if right.Kind() == hff.UnionType {
		return jsonTaggedStringStateAmbiguous(right, left)
	}
	if left.Kind() == hff.AnyType {
		return jsonHasTaggedString(right)
	}
	if right.Kind() == hff.AnyType {
		return jsonHasTaggedString(left)
	}
	if jsonTaggedStringKind(left.Kind()) {
		return right.Kind() == hff.StringType || (jsonTaggedStringKind(right.Kind()) && left.Kind() != right.Kind())
	}
	if jsonTaggedStringKind(right.Kind()) {
		return left.Kind() == hff.StringType
	}
	if left.Kind() == hff.ListType && right.Kind() == hff.ListType {
		return jsonTaggedStringStateAmbiguous(left.Element(), right.Element())
	}
	if (left.Kind() == hff.MapType || left.Kind() == hff.StructType) && (right.Kind() == hff.MapType || right.Kind() == hff.StructType) {
		if left.Kind() == hff.MapType && right.Kind() == hff.MapType {
			return jsonTaggedStringStateAmbiguous(left.Element(), right.Element())
		}
		if left.Kind() == hff.MapType {
			left, right = right, left
		}
		for _, field := range left.Fields() {
			if jsonTaggedStringStateAmbiguous(field.Type, jsonStoredMemberType(right, field.Name, true, false)) {
				return true
			}
		}
	}
	return false
}
