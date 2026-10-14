package sql

import "github.com/ornew/hff"

// Unlike shape queries, retrieved values must retain numeric subtypes. A Map
// has one value schema at every key, so normalize only a selected value after
// validating the complete applicable schema. Dynamic heterogeneous Struct
// access and values/items still normalize the object before combining values.
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
	// Known object carriers already normalize JSON null to SQL null. Only
	// dynamic receivers need a category check before argument presence.
	object := raw
	if r.kind != hff.MapType && r.kind != hff.StructType {
		object = "(CASE WHEN " + string(format) + "_typeof(" + raw + ")='object' THEN " + raw + " ELSE NULL::" + string(format) + " END)"
	}
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
		objectType := jsonObjectType(b.jsonStoredType(child))
		mapOnly := jsonMapObjectsOnly(objectType)
		if constant && stringKey || mapOnly {
			// A literal key's inferred result type is precise. Unused fields
			// must not impose restoration requirements on this selected value.
			// Validate the original Map alternatives before merging their
			// value types. Merging can hide an unsupported tagged/Any union
			// or a numeric distinction that raw JSONB cannot recover.
			if !(constant && stringKey) {
				if err := b.checkComparisonDocumentSchema(child, format, objectType); err != nil {
					return expression{}, err
				}
			}
			selected := "(" + object + " -> " + key + "::text)"
			selectedType := b.jsonStoredType(n)
			if err := b.checkComparisonDocumentSchema(n, format, selectedType); err != nil {
				return expression{}, err
			}
			// Normalization can inspect the same candidate several times.
			// Give selection a name inside this scope before either path.
			selected = b.project(selected)
			present = ordinaryPresence([]expression{{object, r.present, hff.MapType}, {key, arg.present, hff.StringType}})
			if result, handled, err := b.selectedJSONScalar(n, selected, format, selectedType, present); handled {
				return result, err
			}
			code, err = b.normalizedComparisonDocument(n, selected, format, selectedType)
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

// One known scalar family can be decoded from its selected storage directly.
// Keep the category check that normalization would perform: a wrong-category
// JSON value is null rather than an implicit string/number conversion. Unions
// and containers still need schema-guided normalization of their contents.
func (b *builder) selectedJSONScalar(n hff.Node, selected string, format JSONFormat, typ hff.Type, present string) (expression, bool, error) {
	kind, known := jsonKind(typ)
	if !known {
		return expression{}, false, nil
	}
	switch kind {
	case hff.IntType, hff.FloatType, hff.StringType, hff.BoolType, hff.BytesType, hff.DurationType, hff.TimestampType:
		guarded := "(CASE WHEN " + string(format) + "_typeof(" + selected + ")='" + jsonCategory(kind) + "' THEN " + selected + " ELSE NULL::" + string(format) + " END)"
		value, err := b.jsonDecode(n, guarded, format, typ, present)
		return value, true, err
	}
	return expression{}, false, nil
}

// Every applicable object alternative must give all keys a uniform value
// schema. Struct alternatives need their per-key dispatch even when the
// merged get() result happens to have the same type.
func jsonMapObjectsOnly(typ hff.Type) bool {
	if typ.Kind() == hff.MapType {
		return true
	}
	if typ.Kind() != hff.UnionType {
		return false
	}
	members := typ.Members()
	if len(members) == 0 {
		return false
	}
	for _, member := range members {
		if member.Kind() != hff.MapType {
			return false
		}
	}
	return true
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
