package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func structuredKind(k hff.TypeKind) bool {
	return k == hff.ListType || k == hff.StructType || k == hff.MapType
}

// Query collections use JSON, retaining their children as typed SQL arguments.
// No text from the query is inserted into the SQL source.
func (b *builder) jsonLiteral(n hff.Node) (expression, error) {
	var values []expression
	var args []string
	keys, _ := n.Keys()
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		v, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		if n.Kind() == hff.ObjectNode {
			key, err := b.parameter(n, hff.String(keys[i]))
			if err != nil {
				return expression{}, err
			}
			args = append(args, key.code)
		}
		values = append(values, v)
		code := v.code
		if v.kind == hff.NullType {
			code = "NULL::text"
		} else if v.kind == hff.FloatType {
			code = b.floatText(code) + "::json"
		}
		args = append(args, code)
	}
	name, kind := "json_build_array", hff.ListType
	if n.Kind() == hff.ObjectNode {
		name, kind = "json_build_object", hff.StructType
	}
	code := name + "(" + strings.Join(args, ",") + ")"
	b.jsonValues[code] = JSON
	// A null element is data, so unlike ordinary calls it cannot stop the
	// evaluation of later children. No-value propagates from every child.
	present := "TRUE"
	for _, value := range values {
		present = both(present, value.present)
	}
	return expression{code, present, kind}, nil
}

func (b *builder) jsonComparison(n, l, r hff.Node, left, right expression, op hff.Operator) (expression, error) {
	if op != hff.Equal && op != hff.NotEqual {
		return expression{}, b.failure(n, "JSON collections support equality, not ordering")
	}
	lf, lok := b.jsonValues[left.code]
	rf, rok := b.jsonValues[right.code]
	if !lok || !rok {
		return expression{}, b.failure(n, "structural equality requires JSON collection representations")
	}
	equal, err := b.jsonEqual(n, left.code, lf, l.Type(), right.code, rf, r.Type())
	if err != nil {
		return expression{}, err
	}
	if op == hff.NotEqual {
		equal = "(NOT " + equal + ")"
	}
	// Nested nulls compare equal, but top-level nullable comparisons do not.
	valid := both(both(left.present, right.present), both("("+left.code+" IS NOT NULL)", "("+right.code+" IS NOT NULL)"))
	return boolean(both(valid, equal)), nil
}

func jsonCategory(k hff.TypeKind) string {
	switch k {
	case hff.IntType, hff.FloatType:
		return "number"
	case hff.BoolType:
		return "boolean"
	case hff.StringType:
		return "string"
	case hff.ListType:
		return "array"
	case hff.StructType, hff.MapType:
		return "object"
	}
	return ""
}

// Each recursion owns its lateral projections. They must remain inside the
// element/key scope that supplies the raw JSON expressions.
func (b *builder) jsonEqual(n hff.Node, left string, lf JSONFormat, lt hff.Type, right string, rf JSONFormat, rt hff.Type) (code string, err error) {
	if err = checkContext(b.ctx); err != nil {
		return "", err
	}
	outer := b.steps
	b.steps = nil
	defer func() { b.steps = outer }()
	code, err = b.jsonEqualValue(n, left, lf, lt, right, rf, rt)
	if err != nil {
		return "", err
	}
	if len(b.steps) > 0 {
		code = "(SELECT " + code + " FROM " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
	}
	return code, nil
}

func (b *builder) jsonEqualValue(n hff.Node, left string, lf JSONFormat, lt hff.Type, right string, rf JSONFormat, rt hff.Type) (string, error) {
	lmembers, err := b.jsonEqualityMembers(n, lt)
	if err != nil {
		return "", err
	}
	rmembers, err := b.jsonEqualityMembers(n, rt)
	if err != nil {
		return "", err
	}
	lcat := string(lf) + "_typeof(" + left + ")"
	rcat := string(rf) + "_typeof(" + right + ")"
	lnull := "(" + left + " IS NULL OR " + lcat + "='null')"
	rnull := "(" + right + " IS NULL OR " + rcat + "='null')"
	code := "CASE WHEN " + lnull + " THEN " + rnull + " WHEN " + rnull + " THEN FALSE"
	for _, a := range lmembers {
		for _, c := range rmembers {
			if jsonCategory(a.Kind()) != jsonCategory(c.Kind()) {
				continue
			}
			value, err := b.jsonEqualTyped(n, left, lf, a, right, rf, c)
			if err != nil {
				return "", err
			}
			condition := both(lcat+"='"+jsonCategory(a.Kind())+"'", rcat+"='"+jsonCategory(c.Kind())+"'")
			code += " WHEN " + condition + " THEN " + value
		}
	}
	return "(" + code + " ELSE FALSE END)", nil
}

func (b *builder) jsonEqualityMembers(n hff.Node, typ hff.Type) ([]hff.Type, error) {
	members := []hff.Type{typ}
	if typ.Kind() == hff.UnionType {
		members = typ.Members()
	}
	var result []hff.Type
	seen := map[string]int{}
	for _, t := range members {
		if t.Kind() == hff.NullType || t.Kind() == hff.NeverType {
			continue
		}
		category := jsonCategory(t.Kind())
		if category == "" {
			return nil, b.failure(n, "structural equality requires JSON-distinguishable schema members; ambiguous numeric or structural unions need type metadata")
		}
		if index, exists := seen[category]; exists {
			previous := result[index]
			switch category {
			case "array":
				result[index] = hff.NewListType(hff.NewUnionType(previous.Element(), t.Element()))
			case "object":
				var values []hff.Type
				for _, object := range []hff.Type{previous, t} {
					if object.Kind() == hff.MapType {
						values = append(values, object.Element())
					} else {
						for _, field := range object.Fields() {
							values = append(values, field.Type)
						}
					}
				}
				result[index] = hff.NewMapType(hff.NewUnionType(values...))
			default:
				return nil, b.failure(n, "structural equality requires unambiguous numeric subtypes or explicit type metadata")
			}
			continue
		}
		seen[category] = len(result)
		result = append(result, t)
	}
	return result, nil
}

func (b *builder) jsonEqualTyped(n hff.Node, left string, lf JSONFormat, lt hff.Type, right string, rf JSONFormat, rt hff.Type) (string, error) {
	if lt.Kind() == hff.ListType {
		la, ra := b.nextAlias(), b.nextAlias()
		equal, err := b.jsonEqual(n, la+".value", lf, lt.Element(), ra+".value", rf, rt.Element())
		if err != nil {
			return "", err
		}
		from := string(lf) + "_array_elements(" + left + ") WITH ORDINALITY AS " + la + "(value,ord) FULL JOIN " + string(rf) + "_array_elements(" + right + ") WITH ORDINALITY AS " + ra + "(value,ord) ON " + la + ".ord=" + ra + ".ord"
		return "(NOT EXISTS(SELECT 1 FROM " + from + " WHERE " + la + ".ord IS NULL OR " + ra + ".ord IS NULL OR NOT " + equal + "))", nil
	}
	if lt.Kind() == hff.MapType || lt.Kind() == hff.StructType {
		return b.jsonObjectEqual(n, left, lf, lt, right, rf, rt)
	}
	// CASE protects casts for each runtime member of a heterogeneous list.
	ltext := "(CASE WHEN " + string(lf) + "_typeof(" + left + ")='" + jsonCategory(lt.Kind()) + "' THEN " + left + " #>> '{}'::text[] END)"
	rtext := "(CASE WHEN " + string(rf) + "_typeof(" + right + ")='" + jsonCategory(rt.Kind()) + "' THEN " + right + " #>> '{}'::text[] END)"
	l := expression{"(" + ltext + "::" + sqlType(lt.Kind()) + ")", "TRUE", lt.Kind()}
	r := expression{"(" + rtext + "::" + sqlType(rt.Kind()) + ")", "TRUE", rt.Kind()}
	if lt.Kind() != rt.Kind() {
		return b.mixedComparison(l, r, hff.Equal).code, nil
	}
	if lt.Kind() == hff.StringType {
		l.code += " COLLATE \"C\""
		r.code += " COLLATE \"C\""
	}
	return "COALESCE((" + l.code + "=" + r.code + "),FALSE)", nil
}

func objectFieldType(t hff.Type, key string) (hff.Type, bool) {
	if t.Kind() == hff.MapType {
		return t.Element(), true
	}
	for _, field := range t.Fields() {
		if field.Name == key {
			return field.Type, true
		}
	}
	return hff.Type{}, false
}

func (b *builder) jsonObjectEqual(n hff.Node, left string, lf JSONFormat, lt hff.Type, right string, rf JSONFormat, rt hff.Type) (string, error) {
	la, ra := b.nextAlias(), b.nextAlias()
	equal := "FALSE"
	if lt.Kind() == hff.MapType && rt.Kind() == hff.MapType {
		var err error
		equal, err = b.jsonEqual(n, la+".value", lf, lt.Element(), ra+".value", rf, rt.Element())
		if err != nil {
			return "", err
		}
	} else {
		fields := lt.Fields()
		if lt.Kind() == hff.MapType {
			fields = rt.Fields()
		}
		equal = "CASE"
		for _, f := range fields {
			a, aok := objectFieldType(lt, f.Name)
			c, cok := objectFieldType(rt, f.Name)
			if !aok || !cok {
				continue
			}
			key, err := b.parameter(n, hff.String(f.Name))
			if err != nil {
				return "", err
			}
			value, err := b.jsonEqual(n, la+".value", lf, a, ra+".value", rf, c)
			if err != nil {
				return "", err
			}
			equal += " WHEN " + la + ".key COLLATE \"C\"=" + key.code + " COLLATE \"C\" THEN " + value
		}
		if equal == "CASE" {
			equal = "FALSE"
		} else {
			equal = "(" + equal + " ELSE FALSE END)"
		}
	}
	from := string(lf) + "_each(" + left + ") AS " + la + "(key,value) FULL JOIN " + string(rf) + "_each(" + right + ") AS " + ra + "(key,value) ON " + la + ".key COLLATE \"C\"=" + ra + ".key COLLATE \"C\""
	return "(NOT EXISTS(SELECT 1 FROM " + from + " WHERE " + la + ".key IS NULL OR " + ra + ".key IS NULL OR NOT " + equal + "))", nil
}
