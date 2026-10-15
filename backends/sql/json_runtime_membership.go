package sql

import (
	"errors"
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) deferredMembership(n hff.Node) (expression, bool, error) {
	method, ok := n.Method()
	if !ok || !b.hasDeferredValueSource(n) {
		return expression{}, false, nil
	}
	name := method.Name()
	if name != "in" && name != "contains" && name != "unique" {
		return expression{}, false, nil
	}
	for _, signature := range method.Signatures() {
		if _, custom := signature.Declaration(); custom {
			return expression{}, false, nil
		}
	}
	normal := b.validationBuilder()
	normal.keepDeferredValues = false
	result, err := normal.call(n)
	if err == nil {
		b.adoptDeferredProbe(normal)
		return result, true, nil
	}
	if err := b.typedFallbackError(err); err != nil {
		return expression{}, true, err
	}
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	values := make([]*jsonDeferredValue, n.ChildCount())
	codes := make([]string, len(values))
	for i := range values {
		child, _ := n.Child(i)
		parameter, err := probe.deferredValue(child)
		if err != nil {
			return expression{}, true, err
		}
		values[i] = deferredLeaf(parameter)
		code, err := probe.encodeRuntimeValue(child, values[i])
		if err != nil {
			return expression{}, true, err
		}
		codes[i] = probe.project(code)
	}
	result, err = probe.runtimeMembership(n, name, values, codes)
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return result, true, err
}

// Only a backend lowering failure may select a different representation.
// Cancellation, limits and unexpected errors must retain their original state.
func (b *builder) typedFallbackError(err error) error {
	var backendError *hff.Error
	if !errors.As(err, &backendError) || backendError.Code != hff.BackendError {
		return err
	}
	return checkContext(b.ctx)
}

func (b *builder) runtimeMembership(n hff.Node, name string, values []*jsonDeferredValue, codes []string) (expression, error) {
	receiver, tag := codes[0], "("+codes[0]+"->>0)"
	argumentsPresent := "TRUE"
	for _, value := range values[1:] {
		argumentsPresent = both(argumentsPresent, value.present)
	}
	present := both(values[0].present, "("+tag+"='null' OR "+argumentsPresent+")")
	if name == "in" {
		var matches []string
		for i, code := range codes[1:] {
			equal, err := b.runtimeMembershipEqual(n, receiver, code, values[i+1].typ)
			if err != nil {
				return expression{}, err
			}
			matches = append(matches, equal)
		}
		code := "(CASE WHEN " + tag + "='null' THEN NULL::boolean ELSE (" + strings.Join(matches, " OR ") + ") END)"
		return expression{code, present, hff.BoolType}, nil
	}
	if name == "unique" {
		rows, cte := b.runtimeMembershipRows(receiver)
		l, r := b.nextAlias(), b.nextAlias()
		equal, err := b.runtimeTreeEqual(n, l+".value", r+".value")
		if err != nil {
			return expression{}, err
		}
		code := "(CASE WHEN " + tag + "='list' THEN (" + cte + " SELECT NOT EXISTS(SELECT 1 FROM " + rows + " AS " + l + " CROSS JOIN " + rows + " AS " + r + " WHERE " + l + ".ord<" + r + ".ord AND " + equal + ")) ELSE NULL::boolean END)"
		return expression{code, values[0].present, hff.BoolType}, nil
	}
	method, _ := n.Method()
	var listAllowed, stringAllowed bool
	for _, signature := range method.Signatures() {
		listAllowed = listAllowed || signature.ID() == "list.contains"
		stringAllowed = stringAllowed || signature.ID() == "string.contains"
	}
	code := "CASE WHEN " + tag + "='null' THEN NULL::boolean"
	presence := "CASE WHEN " + tag + "='null' THEN " + values[0].present
	if listAllowed {
		rows, cte := b.runtimeMembershipRows(receiver)
		var matches []string
		for i, needle := range codes[1:] {
			alias := b.nextAlias()
			equal, err := b.runtimeMembershipEqual(n, alias+".value", needle, values[i+1].typ)
			if err != nil {
				return expression{}, err
			}
			matches = append(matches, "EXISTS(SELECT 1 FROM "+rows+" AS "+alias+" WHERE "+equal+")")
		}
		code += " WHEN " + tag + "='list' THEN (" + cte + " SELECT (" + strings.Join(matches, " AND ") + "))"
		presence += " WHEN " + tag + "='list' THEN " + present
	}
	if stringAllowed {
		applicable, stringPresent := "TRUE", values[0].present
		var matches []string
		for i, needle := range codes[1:] {
			stringPresent = both(stringPresent, "((NOT "+applicable+") OR "+values[i+1].present+")")
			applicable = both(applicable, "("+needle+"->>0='string')")
			matches = append(matches, "strpos("+receiver+"->1 #>> '{}',"+needle+"->1 #>> '{}')>0")
		}
		code += " WHEN " + tag + "='string' THEN CASE WHEN " + applicable + " THEN (" + strings.Join(matches, " AND ") + ") ELSE NULL::boolean END"
		presence += " WHEN " + tag + "='string' THEN " + stringPresent
	}
	return expression{"(" + code + " ELSE NULL::boolean END)", "(" + presence + " ELSE " + values[0].present + " END)", hff.BoolType}, nil
}

// A null candidate only observes the root tag. Do not expand either tree
// or generate unrelated scalar decoders for this common membership test.
func (b *builder) runtimeMembershipEqual(n hff.Node, value, needle string, needleType hff.Type) (string, error) {
	if needleType.Kind() == hff.NullType {
		return "(" + value + "->>0='null')", checkContext(b.ctx)
	}
	return b.runtimeTreeEqual(n, value, needle)
}

// Both aliases used by equality share these immutable rows. A single global
// ordinal retains the order already established by the encoded list plan.
func (b *builder) runtimeMembershipRows(receiver string) (string, string) {
	rows, source := b.nextAlias(), b.nextAlias()
	payload := "(CASE WHEN " + receiver + "->>0='list' THEN " + receiver + "->1 ELSE '[]'::json END)"
	cte := "WITH " + rows + " AS MATERIALIZED (SELECT value,ord FROM json_array_elements(" + payload + ") WITH ORDINALITY AS " + source + "(value,ord))"
	return rows, cte
}

func (b *builder) runtimeDistinctDescriptor(n hff.Node) (*jsonDeferredValue, error) {
	normal := b.validationBuilder()
	normal.keepDeferredValues = false
	result, err := normal.jsonDistinct(n)
	if err == nil {
		b.adoptDeferredProbe(normal)
		return deferredLeaf(jsonParameter{typ: b.jsonStoredType(n), direct: &result, format: b.jsonValues[result.code],
			lossy: b.losesFloat(n, nil), itemRoles: b.jsonItemRoles(n), itemListRoles: b.jsonItemsRoles(n)}), nil
	}
	if err := b.typedFallbackError(err); err != nil {
		return nil, err
	}
	probe := b.validationBuilder()
	probe.keepDeferredValues = true
	receiver, _ := n.Child(0)
	parameter, err := probe.deferredValue(receiver)
	if err != nil {
		return nil, err
	}
	value := deferredLeaf(parameter)
	code, err := probe.encodeRuntimeValue(receiver, value)
	if err != nil {
		return nil, err
	}
	code = probe.project(code)
	rows, cte := probe.runtimeMembershipRows(code)
	l, r := probe.nextAlias(), probe.nextAlias()
	equal, err := probe.runtimeTreeEqual(n, l+".value", r+".value")
	if err != nil {
		return nil, err
	}
	payload := "(" + cte + " SELECT COALESCE(json_agg(" + l + ".value ORDER BY " + l + ".ord),'[]'::json) FROM " + rows + " AS " + l + " WHERE NOT EXISTS(SELECT 1 FROM " + rows + " AS " + r + " WHERE " + r + ".ord<" + l + ".ord AND " + equal + "))"
	output := "(CASE WHEN " + code + "->>0='list' THEN " + runtimeNode("list", payload) + " ELSE " + runtimeNull + " END)"
	resultValue := runtimeValue(probe.project(output), n.Type(), value.present, deferredValueLoss(value))
	b.adoptDeferredProbe(probe)
	return resultValue, nil
}
