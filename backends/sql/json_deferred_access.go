package sql

import (
	"maps"
	"strconv"
	"strings"

	"github.com/ornew/hff"
)

// An inapplicable runtime branch still undergoes backend validation. Validate
// its argument in an isolated builder so discarded work cannot leave aliases,
// cached expressions, or unused bound parameters in the generated statement.
func (b *builder) validationBuilder() builder {
	probe := *b
	probe.args = append([]any(nil), b.args...)
	probe.steps = append([]string(nil), b.steps...)
	probe.predicates = maps.Clone(b.predicates)
	probe.bindings = maps.Clone(b.bindings)
	probe.qualifiedNativePaths = maps.Clone(b.qualifiedNativePaths)
	probe.reserved = maps.Clone(b.reserved)
	probe.methods = maps.Clone(b.methods)
	probe.compositeBindings = maps.Clone(b.compositeBindings)
	probe.jsonBindings = maps.Clone(b.jsonBindings)
	probe.structBindings = maps.Clone(b.structBindings)
	probe.relationBindings = maps.Clone(b.relationBindings)
	probe.jsonValues = maps.Clone(b.jsonValues)
	probe.normalizedJSONValues = maps.Clone(b.normalizedJSONValues)
	probe.parameters = copyJSONParameters(b.parameters)
	probe.itemRoleMemo = nil
	probe.sharedPaths = maps.Clone(b.sharedPaths)
	probe.physicalParameters = maps.Clone(b.physicalParameters)
	probe.parameterItems = maps.Clone(b.parameterItems)
	probe.parameterProvenance = maps.Clone(b.parameterProvenance)
	probe.activeRelationAliases = maps.Clone(b.activeRelationAliases)
	probe.orderBlind = maps.Clone(b.orderBlind)
	probe.literalConversions = maps.Clone(b.literalConversions)
	probe.identityAliases = maps.Clone(b.identityAliases)
	probe.operationKeys = maps.Clone(b.operationKeys)
	return probe
}

func (b *builder) validateDeferredIndex(index *deferredAccessIndex) error {
	if index.lowered != nil || !index.node.Valid() {
		return checkContext(b.ctx)
	}
	probe := b.validationBuilder()
	_, err := probe.expression(index.node)
	return err
}

func withDeferredPresence(value *jsonDeferredValue, present string) *jsonDeferredValue {
	copy := *value
	copy.present = both(present, value.present)
	return &copy
}

// Access selects a descriptor, not a JSON category. A dynamic key or position
// keeps alternatives separate until a later guard or observer resolves them.
type deferredAccessIndex struct {
	node          hff.Node
	key           string
	knownKey      bool
	position      float64
	knownPosition bool
	lowered       *expression
}

func (b *builder) lowerDeferredIndex(index *deferredAccessIndex) (expression, error) {
	if index.lowered != nil {
		return *index.lowered, nil
	}
	var result expression
	var err error
	if !index.node.Valid() {
		result, err = b.parameter(hff.Node{}, hff.String(index.key))
	} else {
		result, err = b.expression(index.node)
	}
	if err == nil {
		index.lowered = &result
	}
	return result, err
}

func (b *builder) deferredAccess(n hff.Node, value *jsonDeferredValue) (*jsonDeferredValue, error) {
	index := &deferredAccessIndex{}
	if n.Kind() == hff.FieldNode {
		index.key, _ = n.Field()
		index.knownKey = true
	} else {
		index.node, _ = n.Child(1)
		if literal, ok := index.node.Literal(); ok {
			index.key, index.knownKey = literal.AsString()
		}
		index.position, index.knownPosition = itemLiteralIndex(index.node)
	}
	if index.knownKey {
		if err := b.checkTextParameter(n, index.key); err != nil {
			return nil, err
		}
	}
	return b.accessDeferredIndex(n, value, index)
}

func makeDeferredChoices(choices []jsonDeferredChoice, present string) *jsonDeferredValue {
	retained := make([]jsonDeferredChoice, 0, len(choices))
	for _, choice := range choices {
		if choice.when != "FALSE" {
			retained = append(retained, choice)
		}
	}
	choices = retained
	parts := make([]string, len(choices))
	for i, choice := range choices {
		parts[i] = choice.value.present
	}
	return &jsonDeferredValue{typ: deferredChoiceType(choices), present: both(present, deferredCases(choices, parts, "TRUE")), choices: choices}
}

func (b *builder) accessDeferredIndex(n hff.Node, value *jsonDeferredValue, index *deferredAccessIndex) (*jsonDeferredValue, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, err
	}
	if value.runtime != nil {
		return b.accessRuntimeValue(n, value, index)
	}
	if value.selection != nil {
		if !index.knownKey && (!index.knownPosition || index.node.Kind() != hff.LiteralNode) {
			if _, err := b.lowerDeferredIndex(index); err != nil {
				return nil, err
			}
		}
		return b.mapDeferredSelection(n, value, func(child *jsonDeferredValue) (*jsonDeferredValue, error) {
			return b.accessDeferredIndex(n, child, index)
		})
	}
	if value.choices != nil {
		if len(value.choices) == 0 {
			return withDeferredPresence(deferredNull(), value.present), b.validateDeferredIndex(index)
		}
		choices := make([]jsonDeferredChoice, len(value.choices))
		for i, choice := range value.choices {
			child, err := b.accessDeferredIndex(n, choice.value, index)
			if err != nil {
				return nil, err
			}
			choices[i] = jsonDeferredChoice{choice.when, child}
		}
		return makeDeferredChoices(choices, value.present), nil
	}
	method, call := n.Method()
	get := call && method.Name() == "get"
	if get && value.list != nil {
		// Method dispatch precedes argument evaluation. Bracket indexing also
		// accepts lists, but object.get must not index an inapplicable branch.
		return withDeferredPresence(deferredNull(), value.present), b.validateDeferredIndex(index)
	}
	if value.object != nil {
		if index.knownKey {
			for _, field := range *value.object {
				if field.name == index.key {
					return withDeferredPresence(field.value, value.present), nil
				}
			}
			return withDeferredPresence(deferredNull(), value.present), nil
		}
		operand, err := b.lowerDeferredIndex(index)
		if err != nil {
			return nil, err
		}
		stringMatch, stringKey := "FALSE", ""
		if operand.kind == hff.AnyType {
			raw, format, err := b.jsonRepresentation(index.node, operand)
			if err != nil {
				return nil, err
			}
			stringMatch = b.jsonStringMatch(index.node, raw, format)
			stringKey = "(" + raw + " #>> '{}')"
		}
		choices := []jsonDeferredChoice{}
		for _, field := range *value.object {
			condition := "FALSE"
			if operand.kind == hff.StringType || operand.kind == hff.AnyType {
				key, err := b.parameter(n, hff.String(field.name))
				if err != nil {
					return nil, err
				}
				if operand.kind == hff.StringType {
					condition = "(" + operand.code + "=" + key.code + " COLLATE \"C\")"
				} else {
					condition = both(stringMatch, "("+stringKey+"="+key.code+" COLLATE \"C\")")
				}
			}
			choices = append(choices, jsonDeferredChoice{condition, field.value})
		}
		return makeDeferredChoices(choices, both(value.present, operand.present)), nil
	}
	if value.list != nil && value.list.literal {
		arms := []jsonDeferredArm{}
		for _, arm := range value.list.arms {
			if !arm.empty {
				arms = append(arms, arm)
			}
		}
		if index.knownPosition {
			position := index.position
			if position < 0 {
				position += float64(len(arms))
			}
			for i, arm := range arms {
				if position == float64(i) {
					return withDeferredPresence(deferredLeaf(arm.value), value.present), nil
				}
			}
			return withDeferredPresence(deferredNull(), value.present), nil
		}
		operand, err := b.lowerDeferredIndex(index)
		if err != nil {
			return nil, err
		}
		numeric, err := b.numericOperand(index.node, operand)
		if err != nil {
			return nil, err
		}
		choices := []jsonDeferredChoice{}
		for i, arm := range arms {
			positions := "(" + strconv.Itoa(i) + "," + strconv.Itoa(i-len(arms)) + ")"
			var tests []string
			if numeric.integerPossible {
				tests = append(tests, both(numeric.isInteger, "("+numeric.integer.code+" IN "+positions+")"))
			}
			if numeric.floatPossible {
				tests = append(tests, both(numeric.isFloat, "("+numeric.floating.code+" IN "+positions+")"))
			}
			condition := "FALSE"
			if len(tests) > 0 {
				condition = "(" + strings.Join(tests, " OR ") + ")"
			}
			choices = append(choices, jsonDeferredChoice{condition, deferredLeaf(arm.value)})
		}
		return makeDeferredChoices(choices, both(value.present, "("+value.list.null+" OR "+operand.present+")")), nil
	}
	if value.list != nil {
		return b.selectDeferredValue(n, value, index)
	}

	if value.leaf != nil {
		parent, err := b.deferredLeafExpression(n, *value.leaf)
		if err != nil {
			return nil, err
		}
		parent.present = value.present
		if parent.kind == hff.NullType {
			return withDeferredPresence(deferredNull(), value.present), b.validateDeferredIndex(index)
		}
		format, structured := b.jsonValues[parent.code]
		if !structured {
			return withDeferredPresence(deferredNull(), value.present), b.validateDeferredIndex(index)
		}
		object := "TRUE"
		if get {
			shape := makeComparisonShape(value.typ)
			if !shape.any && !shape.object {
				return withDeferredPresence(deferredNull(), value.present), b.validateDeferredIndex(index)
			}
			object = "(" + string(format) + "_typeof(" + parent.code + ")='object')"
		}
		operand, err := b.lowerDeferredIndex(index)
		if err != nil {
			return nil, err
		}
		stored := jsonStoredMemberType(value.typ, index.key, index.knownKey, n.Kind() != hff.FieldNode)
		result, err := b.jsonIndexOperands(n, parent, operand, format, presenceBefore(parent, operand.present), stored)
		if get {
			previous := result.code
			result.code = "(CASE WHEN " + object + " THEN " + result.code + " ELSE NULL END)"
			result.present = both(parent.present, "(CASE WHEN "+object+" THEN "+operand.present+" ELSE TRUE END)")
			if physical, structured := b.jsonValues[previous]; structured {
				b.jsonValues[result.code] = physical
				b.copyNormalizedJSON(previous, result.code, physical)
			}
		}
		return deferredLeaf(jsonParameter{typ: stored, direct: &result, lossy: value.leaf.lossy, format: format}), err
	}
	return withDeferredPresence(deferredNull(), value.present), nil
}

func (b *builder) materializeDeferredValue(n hff.Node, value *jsonDeferredValue) (expression, error) {
	if err := checkContext(b.ctx); err != nil {
		return expression{}, err
	}
	if value.runtime != nil {
		return b.materializeRuntimeValue(n, value)
	}
	if value.selection != nil {
		return b.materializeDeferredSelection(n, value)
	}
	if value.leaf != nil {
		result, err := b.deferredLeafExpression(n, *value.leaf)
		result.present = value.present
		return result, err
	}
	if value.list != nil {
		plan, err := b.deferredValueList(n, value)
		if err != nil {
			return expression{}, err
		}
		return b.materializeDeferredList(n, plan)
	}
	if value.object != nil {
		var args []string
		for _, field := range *value.object {
			key, err := b.parameter(n, hff.String(field.name))
			if err != nil {
				return expression{}, err
			}
			child, err := b.materializeDeferredValue(n, field.value)
			if err != nil {
				return expression{}, err
			}
			raw, err := b.computedJSONDocument(n, child, field.value.typ)
			if err != nil {
				return expression{}, err
			}
			args = append(args, key.code, raw)
		}
		raw := "json_build_object(" + strings.Join(args, ",") + ")"
		b.jsonValues[raw] = JSON
		b.rememberNormalizedJSON(raw, JSON, value.typ)
		return expression{raw, value.present, hff.StructType}, nil
	}
	values := make([]string, len(value.choices))
	presence := make([]string, len(value.choices))
	for i, choice := range value.choices {
		for _, previous := range value.choices[:i] {
			if err := checkContext(b.ctx); err != nil {
				return expression{}, err
			}
			if jsonTaggedStringStateAmbiguous(previous.value.typ, choice.value.typ) {
				return expression{}, b.failure(n, "constructed alternatives require recoverable value types")
			}
		}
		child, err := b.materializeDeferredValue(n, choice.value)
		if err != nil {
			return expression{}, err
		}
		presence[i] = child.present
		values[i], err = b.computedJSONDocument(n, child, choice.value.typ)
		if err != nil {
			return expression{}, err
		}
	}
	if err := b.checkJSONStringSchemas(n, value.typ); err != nil {
		return expression{}, err
	}
	raw := b.project(deferredCases(value.choices, values, "'null'::json"))
	return b.jsonDecode(n, raw, JSON, value.typ, both(value.present, deferredCases(value.choices, presence, "TRUE")))
}
