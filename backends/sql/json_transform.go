package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Lower a lexical callback into a row source, isolating its projections from
// the enclosing expression. Each row retains its source ordinal and no-value.
func (b *builder) jsonCallbackRows(n hff.Node, value expression, format JSONFormat, callback hff.Node) (from, ordinal string, result expression, err error) {
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 1 {
		return "", "", expression{}, b.failure(n, "list transformation requires one callback parameter")
	}
	alias := b.nextAlias()
	outer := b.steps
	b.steps = nil
	b.projectionDepth++
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	id := parameters[0].ID()
	previous, existed := b.parameters[id]
	b.parameters[id] = jsonParameter{code: alias + ".value", format: format}
	defer func() {
		b.steps = outer
		b.projectionDepth--
		if existed {
			b.parameters[id] = previous
		} else {
			delete(b.parameters, id)
		}
	}()
	body, _ := callback.Child(0)
	result, err = b.expression(body)
	if err != nil {
		return "", "", expression{}, err
	}
	from = string(format) + "_array_elements(" + value.code + ") WITH ORDINALITY AS " + alias + "(value,ord)"
	if len(b.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	}
	return from, alias + ".ord", result, nil
}

func (b *builder) jsonTransform(n hff.Node, name string) (expression, error) {
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
		return expression{}, b.failure(n, "list transformations require a JSON list receiver")
	}
	callback, _ := n.Child(1)
	from, ordinal, result, err := b.jsonCallbackRows(n, value, format, callback)
	if err != nil {
		return expression{}, err
	}
	raw, _, err := b.jsonRepresentation(n, result)
	if err != nil {
		return expression{}, err
	}
	rows, output := b.nextAlias(), b.nextAlias()
	// MATERIALIZED avoids duplicating callback SQL when collecting both its
	// values and its first failure. Evaluation order itself is not guaranteed.
	cte := "WITH " + rows + " AS MATERIALIZED (SELECT " + ordinal + " AS ord," + raw + "::json AS value," + result.present + " AS p FROM " + from + ")"
	aggregate := "(SELECT COALESCE(json_agg(" + rows + ".value ORDER BY " + rows + ".ord),'[]'::json) FROM " + rows + ")"
	failure := "(SELECT COALESCE(bool_and(" + rows + ".p),TRUE) FROM " + rows + ")"
	valid := "TRUE"
	if name == "flatmap" {
		// Null/non-list results terminate flatmap before a later no-value.
		// Retaining the first failed ordinal reproduces that distinction.
		invalid := "(" + rows + ".value IS NULL OR json_typeof(" + rows + ".value) IS DISTINCT FROM 'array')"
		first := b.nextAlias()
		cte += ", " + first + " AS (SELECT p FROM " + rows + " WHERE NOT p OR " + invalid + " ORDER BY ord LIMIT 1)"
		failure = "COALESCE((SELECT p FROM " + first + "),TRUE)"
		valid = "(NOT EXISTS(SELECT 1 FROM " + first + "))"
		items := b.nextAlias()
		// A guarded CASE also protects array_elements when a callback has a
		// scalar member or null; that failure is returned by the method itself.
		array := "(CASE WHEN json_typeof(" + rows + ".value)='array' THEN " + rows + ".value ELSE '[]'::json END)"
		aggregate = "(SELECT COALESCE(json_agg(" + items + ".value ORDER BY " + rows + ".ord," + items + ".ord),'[]'::json) FROM " + rows + " CROSS JOIN LATERAL json_array_elements(" + array + ") WITH ORDINALITY AS " + items + "(value,ord))"
	}
	code := "(CASE WHEN " + value.code + " IS NULL OR NOT " + valid + " THEN NULL ELSE " + aggregate + " END)"
	present := both(value.present, "("+value.code+" IS NULL OR "+failure+")")
	b.steps = append(b.steps, "("+cte+" SELECT "+code+" AS v,"+present+" AS p OFFSET 0) AS "+output)
	b.jsonValues[output+".v"] = JSON
	return expression{output + ".v", output + ".p", hff.ListType}, nil
}
