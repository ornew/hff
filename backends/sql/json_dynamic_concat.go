package sql

import "github.com/ornew/hff"

// Concatenation retains element subtypes, including those observable by later
// indexing and casts. Normalize each admitted list schema before aggregation.
func (b *builder) dynamicJSONConcat(n hff.Node, values []expression) (expression, error) {
	receiver := values[0]
	if receiver.kind == hff.StringType {
		return b.dynamicJSONStringCall(n, "concat", values)
	}
	if receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	method, _ := n.Method()
	var stringsAllowed, listsAllowed bool
	for _, signature := range method.Signatures() {
		stringsAllowed = stringsAllowed || signature.ID() == "string.concat"
		listsAllowed = listsAllowed || signature.ID() == "list.concat"
	}
	raw, format, err := b.jsonRepresentation(n, receiver)
	if err != nil {
		return expression{}, err
	}
	class := string(format) + "_typeof(" + raw + ")"
	stored := b.jsonStoredType(n)
	stable := true
	code := "CASE WHEN " + receiver.code + " IS NULL THEN NULL::json"
	present := "CASE WHEN " + receiver.code + " IS NULL THEN " + receiver.present
	if stringsAllowed {
		value, err := b.dynamicJSONStringCall(n, "concat", values)
		if err != nil {
			return expression{}, err
		}
		preserves, err := b.normalizationPreservesJSON(hff.PrimitiveType(hff.StringType), stored)
		if err != nil {
			return expression{}, err
		}
		stable = stable && preserves
		code += " WHEN " + class + "='string' THEN to_json(" + value.code + ")"
		present += " WHEN " + class + "='string' THEN " + value.present
	}
	if listsAllowed {
		converted := append([]expression(nil), values...)
		schemas := make([]hff.Type, len(values))
		for i, value := range values {
			child, _ := n.Child(i)
			if value.kind == hff.NullType {
				continue
			}
			raw, physical, err := b.jsonRepresentation(child, value)
			if err != nil {
				return expression{}, err
			}
			schemas[i] = containsListType(child.Type())
			document, err := b.concatOperandDocument(child, raw, physical, schemas[i])
			if err != nil {
				return expression{}, err
			}
			document = b.project(document)
			list := "(CASE WHEN json_typeof(" + document + ")='array' THEN " + document + " ELSE NULL::json END)"
			b.jsonValues[list] = JSON
			converted[i] = expression{list, value.present, hff.ListType}
		}
		value, err := b.concatJSONLists(n, converted, JSON, schemas)
		if err != nil {
			return expression{}, err
		}
		preserves, err := b.canReuseNormalizedJSON(value.code, JSON, stored)
		if err != nil {
			return expression{}, err
		}
		stable = stable && preserves
		code += " WHEN " + class + "='array' THEN " + value.code
		present += " WHEN " + class + "='array' THEN " + value.present
	}
	code = "(" + code + " ELSE NULL::json END)"
	present = "(" + present + " ELSE " + receiver.present + " END)"
	if stable {
		allowed, err := b.normalizationProofSchema(n, stored)
		if err != nil {
			return expression{}, err
		}
		if allowed {
			b.rememberNormalizedJSON(code, JSON, stored)
		}
	}
	return b.jsonDecode(n, code, JSON, n.Type(), present)
}
