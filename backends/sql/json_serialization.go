package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) serialize(n hff.Node, value expression, name string) (expression, error) {
	var code string
	if format, ok := b.jsonValues[value.code]; ok {
		receiver, _ := n.Child(0)
		if b.losesFloat(receiver, nil) {
			return expression{}, b.failure(n, "JSONB cannot preserve signed zero for float serialization")
		}
		var err error
		stored := b.jsonStoredType(receiver)
		if needsDynamicJSONEquality(stored) {
			var normalized string
			normalized, err = b.comparisonDocument(receiver, value.code, format, stored)
			if err == nil {
				code = b.canonicalJSON(b.project(normalized))
			}
		} else {
			code, err = b.serializeJSON(n, value.code, format, stored)
		}
		if err != nil {
			return expression{}, err
		}
		if name == "string" {
			code = "(CASE WHEN " + string(format) + "_typeof(" + value.code + ")='string' THEN (" + value.code + " #>> '{}'::text[]) ELSE " + code + " END)"
		}
	} else {
		switch value.kind {
		case hff.NullType:
			code = "'null'::text"
		case hff.BoolType, hff.IntType:
			code = "COALESCE(" + value.code + "::text,'null')"
		case hff.FloatType:
			receiver, _ := n.Child(0)
			if b.losesFloat(receiver, nil) {
				return expression{}, b.failure(n, "JSONB cannot preserve signed zero for float serialization")
			}
			code = b.floatText(value.code)
		case hff.StringType:
			code = value.code
			if name == "json" {
				code = "to_json(" + code + ")::text"
			}
			code = "COALESCE(" + code + ",'null')"
		default:
			return expression{}, b.failure(n, "PostgreSQL serialization cannot yet preserve this value's canonical representation")
		}
	}
	return expression{code, value.present, hff.StringType}, nil
}

func jsonHasFloat(typ hff.Type) bool {
	switch typ.Kind() {
	case hff.FloatType:
		return true
	case hff.ListType, hff.MapType:
		return jsonHasFloat(typ.Element())
	case hff.StructType:
		for _, field := range typ.Fields() {
			if jsonHasFloat(field.Type) {
				return true
			}
		}
	case hff.UnionType:
		for _, member := range typ.Members() {
			if jsonHasFloat(member) {
				return true
			}
		}
	}
	return false
}

// Track float provenance through callbacks: a map that returns a constant does
// not inherit a representation loss from its receiver's unused elements.
func (b *builder) losesFloat(n hff.Node, environment map[hff.BindingID]bool) bool {
	if !jsonHasFloat(n.Type()) {
		return false
	}
	if mapping, exists := b.relationMapping(n, b.physicalParameters); exists {
		return b.bindingLosesFloat(mapping, n.Type())
	}
	if mapping, exists := b.relationMapping(n, b.parameterProvenance); exists {
		return b.bindingLosesFloat(mapping, n.Type())
	}
	if binding, ok := n.Binding(); ok {
		if loss, exists := environment[binding.ID()]; exists {
			return loss
		}
		if parameter, exists := b.parameters[binding.ID()]; exists && (parameter.format == JSONB || parameter.lossy) {
			return true
		}
	}
	if path, ok := n.Path(); ok {
		for size := len(path); size > 0; size-- {
			if binding, exists := b.relationBindings[pathKey(path[:size])]; exists {
				if relationLosesFloat(*binding.relation) {
					return true
				}
			}

			if binding, exists := b.structBindings[pathKey(path[:size])]; exists {
				typ, err := boundType(b.program.Schema(), path[:size])
				if err == nil && structLosesFloat(binding, typ, path[size:]) {
					return true
				}
			}
			if b.jsonBindings[pathKey(path[:size])] == JSONB {
				return true
			}
		}
	}
	if method, ok := n.Method(); ok {
		name := method.Name()
		if name == "map" || name == "flatmap" || name == "foldl" || name == "foldr" {
			receiver, _ := n.Child(0)
			callback, _ := n.Child(n.ChildCount() - 1)
			parameters, _ := callback.Parameters()
			nested := make(map[hff.BindingID]bool, len(environment)+len(parameters))
			for id, loss := range environment {
				nested[id] = loss
			}
			receiverLoss := b.losesFloat(receiver, environment)
			seedLoss := false
			if len(parameters) == 1 {
				nested[parameters[0].ID()] = receiverLoss
			} else if len(parameters) == 2 {
				seed, _ := n.Child(1)
				seedLoss = b.losesFloat(seed, environment)
				acc, item := 0, 1
				if name == "foldr" {
					acc, item = 1, 0
				}
				nested[parameters[acc].ID()] = seedLoss
				nested[parameters[item].ID()] = receiverLoss
				body, _ := callback.Child(0)
				// The accumulator's provenance is monotone across iterations.
				nested[parameters[acc].ID()] = seedLoss || b.losesFloat(body, nested)
			}
			body, _ := callback.Child(0)
			return seedLoss || b.losesFloat(body, nested)
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if b.losesFloat(child, environment) {
			return true
		}
	}
	return false
}

// Serialize from schema types, never from the storage document's spelling or
// object order. In particular jsonb::text is not HFF canonical JSON.
func (b *builder) serializeJSON(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	if err := b.ctx.Err(); err != nil {
		return "", err
	}
	members, err := b.jsonEqualityMembers(n, typ)
	if err != nil {
		return "", b.failure(n, "JSON serialization requires recoverable schema types: "+err.Error())
	}
	var branches []string
	for _, member := range members {
		var code string
		switch member.Kind() {
		case hff.IntType:
			code = jsonScalarCast("("+raw+" #>> '{}')", hff.IntType) + "::text"
		case hff.FloatType:
			code = b.floatText(jsonScalarCast("("+raw+" #>> '{}')", hff.FloatType))
		case hff.BoolType:
			code = "((" + raw + " #>> '{}')::boolean)::text"
		case hff.StringType:
			code = "to_json(" + raw + " #>> '{}')::text"
		case hff.ListType:
			alias := b.nextAlias()
			item, err := b.serializeJSON(n, alias+".value", format, member.Element())
			if err != nil {
				return "", err
			}
			code = "(SELECT '[' || COALESCE(string_agg(" + item + ",',' ORDER BY " + alias + ".ord),'') || ']' FROM " + string(format) + "_array_elements(" + raw + ") WITH ORDINALITY AS " + alias + "(value,ord))"
		case hff.MapType, hff.StructType:
			alias := b.nextAlias()
			var item string
			if member.Kind() == hff.MapType {
				item, err = b.serializeJSON(n, alias+".value", format, member.Element())
			} else {
				var fields []string
				for _, field := range member.Fields() {
					key, keyErr := b.parameter(n, hff.String(field.Name))
					if keyErr != nil {
						return "", keyErr
					}
					text, fieldErr := b.serializeJSON(n, alias+".value", format, field.Type)
					if fieldErr != nil {
						return "", fieldErr
					}
					fields = append(fields, "WHEN "+alias+".key = "+key.code+" COLLATE \"C\" THEN "+text)
				}
				item = "'null'::text"
				if len(fields) > 0 {
					item = "(CASE " + strings.Join(fields, " ") + " ELSE 'null' END)"
				}
			}
			if err != nil {
				return "", err
			}
			code = "(SELECT '{' || COALESCE(string_agg(to_json(" + alias + ".key)::text || ':' || " + item + ",',' ORDER BY " + alias + ".key COLLATE \"C\"),'') || '}' FROM " + string(format) + "_each(" + raw + ") AS " + alias + ")"
		default:
			return "", b.failure(n, "PostgreSQL JSON serialization cannot yet preserve canonical float or dynamic value representations")
		}
		branches = append(branches, "WHEN "+string(format)+"_typeof("+raw+") = '"+jsonCategory(member.Kind())+"' THEN "+code)
	}
	if len(branches) == 0 {
		return "'null'::text", nil
	}
	return "(CASE WHEN " + raw + " IS NULL OR " + string(format) + "_typeof(" + raw + ") = 'null' THEN 'null' " + strings.Join(branches, " ") + " ELSE 'null' END)", nil
}
