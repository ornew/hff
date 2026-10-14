package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Statically numeric projections can feed AVG directly from the correlated
// row source. Dynamic families and computed receivers keep the JSON path.
func (b *builder) relationAverage(n hff.Node) (expression, bool, error) {
	receiver, _ := n.Child(0)
	mapping, ok := b.relationCardinalityBinding(receiver)
	if !ok {
		return expression{}, false, nil
	}
	c := mapping.relation.config
	localAlias, _ := quoteColumn(Column(c.alias))
	projected := c.typ
	var callback, body hff.Node
	if n.ChildCount() > 1 {
		callback, _ = n.Child(1)
		body, _ = callback.Child(0)
		projected = body.Type()
	}
	family, known := scalarKind(projected)
	if projected.Kind() == hff.NeverType {
		family, known = hff.NullType, true
	}
	if !known || family != hff.IntType && family != hff.FloatType && family != hff.NullType {
		return expression{}, false, nil
	}
	if b.activeRelationAliases == nil {
		b.activeRelationAliases = make(map[string]int)
	}
	b.activeRelationAliases[c.alias]++
	defer func() { b.activeRelationAliases[c.alias]-- }()
	absent := b.relationAbsence(receiver, c)
	var term expression
	var err error
	localColumn := false
	if callback.Valid() {
		parameters, ok := callback.Parameters()
		if !ok || len(parameters) != 1 {
			return expression{}, true, b.failure(n, "average requires one callback parameter")
		}
		restore := b.relationParameterScope(c, parameters[0].ID())
		defer restore()
		term, err = b.expression(body)
		column, _, native := b.directPhysicalScalarColumn(body)
		localColumn = native && term.code == column && term.present == "TRUE" && strings.HasPrefix(column, localAlias+".")
	} else {
		outerSteps, outerPaths := b.steps, b.sharedPaths
		b.steps, b.sharedPaths = nil, nil
		defer func() { b.steps, b.sharedPaths = outerSteps, outerPaths }()
		if family == hff.NullType {
			term = expression{code: "NULL::bigint", present: "TRUE", kind: hff.NullType}
		} else if c.item.format == "" && !c.item.composite && c.item.structure == nil && c.item.relation == nil && !c.item.timestamp {
			column, columnErr := quoteColumn(c.item.column)
			term, err = expression{code: column, present: "TRUE", kind: family}, columnErr
			localColumn = strings.HasPrefix(column, localAlias+".")
		} else {
			raw, format, encodeErr := b.physicalItemJSON(n, c.item, c.typ)
			if encodeErr != nil {
				return expression{}, true, encodeErr
			}
			term, err = b.jsonDecode(n, raw, format, c.typ, "TRUE")
		}
	}
	if err != nil {
		return expression{}, true, err
	}
	if term.kind != hff.IntType && term.kind != hff.FloatType && term.kind != hff.NullType {
		return expression{}, true, b.failure(n, "average projection requires a numeric SQL carrier")
	}
	if term.kind == hff.NullType {
		term.code = "NULL::bigint"
	}
	table, err := quoteColumn(c.table)
	if err != nil {
		return expression{}, true, err
	}
	alias, err := quoteColumn(Column(c.alias))
	if err != nil {
		return expression{}, true, err
	}
	from := table + " AS " + alias
	if len(b.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	}
	// A local projection keeps AVG at the relation's query level even when
	// its callback references only outer columns. OFFSET 0 prevents flattening
	// from duplicating an expensive scalar before numeric conversion.
	input := term.code
	if !localColumn {
		row := b.nextAlias()
		if term.present != "TRUE" {
			input = "CASE WHEN " + term.present + " THEN " + input + " END"
		}
		from += " CROSS JOIN LATERAL (SELECT " + input + " AS value OFFSET 0) AS " + row
		input = row + ".value"
	}
	numeric := input + "::numeric"
	if term.kind == hff.FloatType {
		numeric = input + "::text::numeric"
	}
	result := b.nextAlias()
	aggregate := "SELECT avg(" + numeric + ") AS value FROM " + from + " WHERE (" + b.predicateCode(c.correlation) + ")"
	code := "(SELECT " + nativeFloatCast(result+".value::text") + " FROM (" + aggregate + ") AS " + result + ")"
	if absent != "FALSE" {
		code = "(CASE WHEN " + absent + " THEN NULL::double precision ELSE " + code + " END)"
	}
	return expression{code: code, present: "TRUE", kind: hff.FloatType}, true, nil
}
