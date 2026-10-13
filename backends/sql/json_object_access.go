package sql

import "github.com/ornew/hff"

// Unlike shape queries, retrieved values must retain numeric subtypes. Apply
// the object schema before selecting keys or combining values into a list.
func (b *builder) jsonObjectAccess(n hff.Node, name string, values []expression) (expression, error) {
	r := values[0]
	if r.kind == hff.NullType {
		return expression{"NULL", r.present, hff.NullType}, nil
	}
	raw, format, err := b.jsonRepresentation(n, r)
	if err != nil {
		return expression{}, err
	}
	child, _ := n.Child(0)
	// Inapplicable receiver families become ordinary null before arguments.
	object := "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='object' THEN " + raw + " ELSE NULL::" + string(format) + " END)"
	normalize := func() (string, error) {
		document, err := b.comparisonDocument(child, raw, format, jsonObjectType(b.jsonStoredType(child)))
		if err != nil {
			return "", err
		}
		document = b.project(document)
		return "(CASE WHEN json_typeof(" + document + ")='object' THEN " + document + " ELSE NULL::json END)", nil
	}
	code := ""
	present := r.present
	if name == "get" {
		arg := values[1]
		if arg.kind == hff.NullType {
			present = ordinaryPresence([]expression{{object, r.present, hff.MapType}, arg})
			return b.jsonDecode(n, "NULL::json", JSON, b.jsonStoredType(n), present)
		}
		key := arg.code
		if arg.kind == hff.AnyType {
			argument, physical, err := b.jsonRepresentation(n, arg)
			if err != nil {
				return expression{}, err
			}
			key = "(CASE WHEN " + string(physical) + "_typeof(" + argument + ")='string' THEN " + argument + " #>> '{}'::text[] ELSE NULL::text END)"
		} else if arg.kind != hff.StringType && arg.kind != hff.NullType {
			return expression{}, b.failure(n, "object get requires a recoverable string key")
		}
		keyNode, _ := n.Child(1)
		literal, constant := keyNode.Literal()
		_, stringKey := literal.AsString()
		if constant && stringKey {
			// A literal key's inferred result type is precise. Unused fields
			// must not impose restoration requirements on this selected value.
			code, err = b.comparisonDocument(n, "("+object+" -> "+key+"::text)", format, b.jsonStoredType(n))
			if err != nil {
				return expression{}, err
			}
		} else {
			object, err = normalize()
			if err != nil {
				return expression{}, err
			}
			code = "(" + object + " -> " + key + "::text)"
		}
		present = ordinaryPresence([]expression{{object, r.present, hff.MapType}, {key, arg.present, hff.StringType}})
	} else {
		object, err = normalize()
		if err != nil {
			return expression{}, err
		}
		alias := b.nextAlias()
		item := alias + ".value"
		if name == "items" {
			item = "json_build_array(" + alias + ".key," + alias + ".value)"
		}
		code = "(CASE WHEN " + object + " IS NULL THEN NULL::json ELSE (SELECT COALESCE(json_agg(" + item + " ORDER BY " + alias + ".key COLLATE \"C\"),'[]'::json) FROM json_each(" + object + ") AS " + alias + "(key,value)) END)"
	}
	return b.jsonDecode(n, code, JSON, b.jsonStoredType(n), present)
}

// Other union members cannot receive object accessors. They must not impose
// unrelated restoration requirements on the object branch.
func jsonObjectType(typ hff.Type) hff.Type {
	if typ.Kind() != hff.UnionType {
		return typ
	}
	var objects []hff.Type
	for _, member := range typ.Members() {
		if member.Kind() == hff.AnyType {
			return member
		}
		if member.Kind() == hff.StructType || member.Kind() == hff.MapType {
			objects = append(objects, member)
		}
	}
	return hff.NewUnionType(objects...)
}
