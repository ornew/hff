package sql

import "github.com/ornew/hff"

// Shape queries do not observe numeric element subtypes, so JSONB can answer
// them without restoring document metadata. Numeric index argument subtypes
// still matter at int64 conversion boundaries. Runtime families determine
// which admitted signature receives the argument.
func (b *builder) dynamicJSONShape(n hff.Node, name string, values []expression) (expression, error) {
	r := values[0]
	if r.kind == hff.NullType {
		return expression{"NULL", r.present, hff.NullType}, nil
	}
	raw, format, err := b.jsonRepresentation(n, r)
	if err != nil {
		return expression{}, err
	}
	method, _ := n.Method()
	var list, object bool
	for _, signature := range method.Signatures() {
		list = list || signature.ID() == "list."+name
		object = object || signature.ID() == "object."+name
	}
	class := string(format) + "_typeof(" + raw + ")"
	kind := hff.IntType
	null := "NULL::bigint"
	if name == "has" {
		kind, null = hff.BoolType, "NULL::boolean"
	} else if name == "keys" {
		kind, null = hff.ListType, "NULL::json"
	}
	code := "CASE WHEN " + r.code + " IS NULL THEN " + null
	present := "CASE WHEN " + r.code + " IS NULL THEN " + r.present
	for _, family := range []string{"array", "object"} {
		if family == "array" && !list || family == "object" && !object {
			continue
		}
		value := ""
		branchPresence := r.present
		switch name {
		case "size":
			value = string(format) + "_array_length(" + raw + ")::bigint"
			if family == "object" {
				value = "(SELECT count(*) FROM " + string(format) + "_object_keys(" + raw + "))"
			}
		case "keys":
			alias := b.nextAlias()
			value = "(SELECT COALESCE(json_agg(" + alias + ".key ORDER BY " + alias + ".key COLLATE \"C\"),'[]'::json) FROM " + string(format) + "_object_keys(" + raw + ") AS " + alias + "(key))"
		case "has":
			arg := values[1]
			converted := arg
			isFloat := "TRUE"
			if arg.kind == hff.AnyType {
				argument, physical, err := b.jsonRepresentation(n, arg)
				if err != nil {
					return expression{}, err
				}
				category, scalar := "string", "text"
				converted.kind = hff.StringType
				if family == "array" {
					category, scalar = "number", "double precision"
					converted.kind = hff.FloatType
					child, _ := n.Child(1)
					integer, floating := jsonNumericCandidates(child.Type())
					if integer && floating && physical != JSON {
						return expression{}, b.failure(n, "dynamic list has requires preserved numeric argument types or type metadata")
					}
					if !floating {
						isFloat = "FALSE"
					} else if integer {
						isFloat = "(" + argument + " #>> '{}'::text[]) ~ '[.eE]'"
					}
				}
				cast := jsonScalarCast("("+argument+" #>> '{}'::text[])", converted.kind)
				converted.code = "(CASE WHEN " + string(physical) + "_typeof(" + argument + ")='" + category + "' THEN " + cast + " ELSE NULL::" + scalar + " END)"
			}
			branchPresence = ordinaryPresence([]expression{r, converted})
			if converted.kind == hff.NullType {
				value = null
			} else if family == "object" && converted.kind == hff.StringType {
				value = "(CASE WHEN " + converted.code + " IS NULL THEN NULL ELSE (" + raw + " -> " + converted.code + "::text IS NOT NULL) END)"
			} else if family == "array" && (converted.kind == hff.IntType || converted.kind == hff.FloatType) {
				length := string(format) + "_array_length(" + raw + ")::bigint"
				integral := "TRUE"
				if converted.kind == hff.FloatType {
					integral = both(converted.code+"=trunc("+converted.code+")", "(NOT ("+isFloat+") OR "+validFloatIndex(converted.code)+")")
				}
				value = "(CASE WHEN " + converted.code + " IS NULL OR NOT (" + integral + ") THEN NULL ELSE (" + converted.code + " >= -" + length + " AND " + converted.code + " < " + length + ") END)"
			} else {
				value = null
			}
		}
		code += " WHEN " + class + "='" + family + "' THEN " + value
		present += " WHEN " + class + "='" + family + "' THEN " + branchPresence
	}
	code = "(" + code + " ELSE " + null + " END)"
	present = "(" + present + " ELSE " + r.present + " END)"
	if kind == hff.ListType {
		b.jsonValues[code] = JSON
	}
	return expression{code, present, kind}, nil
}

// Float indices must be representable as int64 before bounds checking. An
// integer already in int64 has no corresponding conversion failure.
func validFloatIndex(code string) string {
	return "(" + code + " >= '-9223372036854775808'::double precision AND " + code + " < '9223372036854775808'::double precision)"
}
