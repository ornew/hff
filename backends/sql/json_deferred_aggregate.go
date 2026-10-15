package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) deferredAggregateDescriptor(n hff.Node, name string) (*jsonDeferredValue, error) {
	// Preserve the order-independent relation AVG path, including callbacks
	// that capture a typed value from an outer lexical scope.
	if name == "avg" {
		normal := b.validationBuilder()
		if result, handled, err := normal.relationAverage(n); handled {
			if err == nil {
				b.adoptDeferredProbe(normal)
				return deferredLeaf(jsonParameter{typ: n.Type(), direct: &result}), nil
			}
			return nil, err
		}
	}
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	receiver, _ := n.Child(0)
	plan, err := probe.deferredList(receiver)
	if err != nil {
		return nil, err
	}
	safe, err := probe.deferredInputIdentity(&jsonDeferredValue{typ: b.jsonStoredType(receiver), present: plan.present, list: &plan})
	if err != nil {
		return nil, err
	}
	projected := plan
	projected.arms = append([]jsonDeferredArm(nil), plan.arms...)
	callback, _ := n.Child(1)
	if callback.Valid() {
		body, _ := callback.Child(0)
		for i, arm := range projected.arms {
			var value jsonParameter
			// Even a native scalar remains a descriptor in this callback.
			// Otherwise a Number guard can fall into the single-type SQL path.
			arm.value = jsonParameter{typ: arm.value.typ, deferred: deferredLeaf(arm.value)}
			steps, parameters, err := probe.inDeferredArm(arm, callback, func() error {
				var err error
				value, err = probe.deferredValue(body)
				return err
			})
			if err != nil {
				return nil, err
			}
			projected.arms[i].steps, projected.arms[i].parameters = steps, parameters
			projected.arms[i].value = value
		}
	}
	projectionSafe, err := probe.deferredInputIdentity(&jsonDeferredValue{typ: n.Type(), present: plan.present, list: &projected})
	if err != nil {
		return nil, err
	}
	if safe && projectionSafe {
		normal := b.validationBuilder()
		normal.keepDeferredValues = false
		result, err := normal.call(n)
		if err == nil {
			b.adoptDeferredProbe(normal)
			lossy := (name == "min" || name == "max") && b.losesFloat(n, nil)
			return deferredLeaf(jsonParameter{typ: b.jsonStoredType(n), direct: &result, format: b.jsonValues[result.code], lossy: lossy}), nil
		}
		if err := b.typedFallbackError(err); err != nil {
			return nil, err
		}
	}
	if callback.Valid() && len(projected.arms) == 0 {
		// A statically List source can have no emitted arms. A failed native
		// probe must not turn an unsupported callback into an empty result.
		// This runs after the successful static-Null native shortcut above.
		alias := probe.nextAlias()
		x := expression{"NULL", "TRUE", hff.NullType}
		arm := jsonDeferredArm{empty: true, from: "(SELECT 1 WHERE FALSE) AS " + alias,
			ordinal: []string{"0"}, selected: "TRUE", value: jsonParameter{typ: hff.PrimitiveType(hff.NeverType), direct: &x}, parameters: copyJSONParameters(probe.parameters)}
		var value jsonParameter
		body, _ := callback.Child(0)
		steps, parameters, err := probe.inDeferredArm(arm, callback, func() error {
			var err error
			value, err = probe.deferredValue(body)
			return err
		})
		if err != nil {
			return nil, err
		}
		arm.steps, arm.parameters, arm.value = steps, parameters, value
		projected.arms = []jsonDeferredArm{arm}
	}
	value, err := probe.aggregateDeferredRows(n, name, projected)
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return value, err
}

// Project numeric root nodes only. A nonnumeric container invalidates the
// aggregate without serializing or recovering any of its unused children.
func (b *builder) aggregateNumericNode(n hff.Node, value *jsonDeferredValue) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if value.runtime != nil {
		return value.runtime.code, nil
	}
	if value.selection != nil {
		result, err := b.resolveDeferredSelection(n, value, hff.AnyType, func(child *jsonDeferredValue) (expression, error) {
			code, err := b.aggregateNumericNode(n, child)
			return expression{code, child.present, hff.AnyType}, err
		})
		return result.code, err
	}
	if value.choices != nil {
		codes := make([]string, len(value.choices))
		for i, choice := range value.choices {
			var err error
			codes[i], err = b.aggregateNumericNode(n, choice.value)
			if err != nil {
				return "", err
			}
		}
		return deferredCases(value.choices, codes, runtimeNull), nil
	}
	const invalid = `'["invalid",null]'::json`
	if value.object != nil {
		return invalid, nil
	}
	if value.list != nil {
		return "(CASE WHEN " + value.list.null + " THEN " + runtimeNull + " ELSE " + invalid + " END)", nil
	}
	parameter := value.leaf
	if parameter.itemSlot != nil {
		slot := *parameter.itemSlot
		return b.jsonItemSlotOperation(n, slot, runtimeNull, func(source hff.Type) (string, error) {
			return b.aggregateNumericDocument(n, slot.raw, slot.format, source)
		})
	}
	carrier, err := b.deferredLeafExpression(n, *parameter)
	if err != nil {
		return "", err
	}
	raw, format, err := b.jsonRepresentation(n, carrier)
	if err != nil {
		return "", err
	}
	return b.aggregateNumericDocument(n, raw, format, value.typ)
}

func (b *builder) aggregateNumericDocument(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	const invalid = `'["invalid",null]'::json`
	integer, floating := jsonNumericCandidates(typ)
	var types []hff.Type
	if integer {
		types = append(types, hff.PrimitiveType(hff.IntType))
	}
	if floating {
		types = append(types, hff.PrimitiveType(hff.FloatType))
	}
	numeric := invalid
	if len(types) > 0 {
		target := hff.NewUnionType(types...)
		if err := b.checkComparisonDocumentSchema(n, format, target); err != nil {
			return "", err
		}
		normalized, err := b.normalizedComparisonDocument(n, raw, format, target)
		if err != nil {
			return "", err
		}
		normalized = b.project(normalized)
		tag := "'int'::text"
		if floating {
			tag = "'float'::text"
			if integer {
				tag = "(CASE WHEN (" + normalized + " #>> '{}') ~ '[.eE]' THEN 'float'::text ELSE 'int'::text END)"
			}
		}
		numeric = "(CASE WHEN json_typeof(" + normalized + ")='number' THEN json_build_array(" + tag + "," + normalized + ") ELSE " + runtimeNull + " END)"
	}
	class := string(format) + "_typeof(" + raw + ")"
	return "(CASE WHEN " + raw + " IS NULL OR " + class + "='null' THEN " + runtimeNull + " WHEN " + class + "='number' THEN " + numeric + " ELSE " + invalid + " END)", nil
}

func (b *builder) aggregateDeferredRows(n hff.Node, name string, plan jsonDeferredList) (*jsonDeferredValue, error) {
	var rows []string
	lossy := false
	for _, arm := range plan.arms {
		value := deferredLeaf(arm.value)
		var code string
		steps, _, err := b.inDeferredArm(arm, hff.Node{}, func() error {
			var err error
			code, err = b.aggregateNumericNode(n, value)
			return err
		})
		if err != nil {
			return nil, err
		}
		arm.steps = steps
		rows = append(rows, "SELECT "+code+" AS value,"+deferredOrdinal(arm)+" AS ord FROM "+deferredFrom(arm)+" WHERE "+both(arm.selected, value.present))
		lossy = lossy || !arm.empty && deferredValueLoss(value)
	}
	if len(rows) == 0 {
		rows = append(rows, "SELECT "+runtimeNull+" AS value,'{}'::bigint[] AS ord WHERE FALSE")
	}
	source, decoded := b.nextAlias(), b.nextAlias()
	item, tag := source+".value", "("+source+".value->>0)"
	text := "(" + item + "->1 #>> '{}')"
	floatValue := "(CASE WHEN " + tag + "='float' THEN " + jsonFloatCast(text) + " END)"
	from := source + " CROSS JOIN LATERAL (SELECT " + floatValue + " AS floating OFFSET 0) AS " + decoded
	integer := "(CASE WHEN " + tag + "='int' THEN " + text + "::bigint::numeric END)"
	term := "(CASE WHEN " + tag + "='int' THEN " + integer + " WHEN " + tag + "='float' THEN " + decoded + ".floating::text::numeric END)"
	invalid := tag + " NOT IN ('int','float','null')"
	cte := "WITH " + source + " AS MATERIALIZED (" + strings.Join(rows, " UNION ALL ") + ")"
	var output string
	if name == "min" || name == "max" {
		key := "(CASE WHEN " + tag + "='int' THEN " + integer + " WHEN " + tag + "='float' THEN " + numericOrderKey(decoded+".floating") + " END)"
		ranked := b.nextAlias()
		cte += "," + ranked + " AS MATERIALIZED (SELECT " + item + " AS value," + source + ".ord," + key + " AS key," + invalid + " AS invalid FROM " + from + ")"
		selected := "(SELECT value FROM " + ranked + " WHERE key IS NOT NULL ORDER BY key" + extremeDirection(name == "min") + ",ord LIMIT 1)"
		output = "(" + cte + " SELECT CASE WHEN EXISTS(SELECT 1 FROM " + ranked + " WHERE invalid) THEN " + runtimeNull + " ELSE COALESCE(" + selected + "," + runtimeNull + ") END)"
	} else {
		aggregate, result := "sum", b.nextAlias()
		if name == "avg" {
			aggregate = "avg"
		}
		total := result + ".total"
		floatOutput := "CASE WHEN pg_input_is_valid(" + total + "::text,'double precision') THEN " + runtimeNode("float", b.floatText(total+"::double precision")+"::json") + " ELSE " + runtimeNull + " END"
		choice := floatOutput
		if name == "sum" {
			choice = "CASE WHEN " + total + " IS NULL THEN " + runtimeNode("int", "0::bigint") + " WHEN " + result + ".floating THEN " + floatOutput + " WHEN " + total + " BETWEEN '-9223372036854775808'::numeric AND '9223372036854775807'::numeric THEN " + runtimeNode("int", total+"::bigint") + " ELSE " + runtimeNull + " END"
		}
		output = "(" + cte + " SELECT CASE WHEN " + result + ".invalid THEN " + runtimeNull + " ELSE " + choice + " END FROM (SELECT " + aggregate + "(" + term + ") AS total,COALESCE(bool_or(" + tag + "='float' AND " + decoded + ".floating IS NOT NULL),FALSE) AS floating,COALESCE(bool_or(" + invalid + "),FALSE) AS invalid FROM " + from + ") AS " + result + ")"
		lossy = false
	}
	output = "(CASE WHEN " + plan.null + " THEN " + runtimeNull + " ELSE " + output + " END)"
	return runtimeValue(b.project(output), n.Type(), plan.present, lossy), nil
}
