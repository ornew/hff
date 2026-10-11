package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) serialize(n hff.Node, value expression, name string) (expression, error) {
	var code string
	if format, ok := b.jsonValues[value.code]; ok {
		receiver, _ := n.Child(0)
		var err error
		code, err = b.serializeJSON(n, value.code, format, receiver.Type())
		if err != nil {
			return expression{}, err
		}
	} else {
		switch value.kind {
		case hff.NullType:
			code = "'null'::text"
		case hff.BoolType, hff.IntType:
			code = "COALESCE(" + value.code + "::text,'null')"
		case hff.StringType:
			code = value.code
			if name == "tojson" {
				code = "to_json(" + code + ")::text"
			}
			code = "COALESCE(" + code + ",'null')"
		default:
			return expression{}, b.failure(n, "PostgreSQL serialization cannot yet preserve this value's canonical representation")
		}
	}
	return expression{code, value.present, hff.StringType}, nil
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
			code = "((" + raw + " #>> '{}')::bigint)::text"
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
