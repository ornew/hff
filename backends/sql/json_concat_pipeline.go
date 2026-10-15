package sql

import (
	"fmt"
	"strings"

	"github.com/ornew/hff"
)

type jsonPipelineArm struct {
	source hff.Node
	stages []hff.Node
}

// Resolve a consumer in each arm's lexical item-slot scope before joining
// results. A homogeneous JSON array cannot retain String/tagged distinctions
// that are still recoverable from the separate source objects and keys.
func (b *builder) jsonConcatMapPipeline(n hff.Node, name string) (expression, bool, error) {
	receiver, _ := n.Child(0)
	if !listConcatNode(receiver) {
		return expression{}, false, nil
	}
	var arms []jsonPipelineArm
	var collect func(hff.Node)
	collect = func(node hff.Node) {
		if listConcatNode(node) {
			for i := 0; i < node.ChildCount(); i++ {
				child, _ := node.Child(i)
				collect(child)
			}
			return
		}
		source, stages := mapPipelineStages(node)
		arms = append(arms, jsonPipelineArm{source, stages})
	}
	collect(receiver)
	needed := false
	for _, arm := range arms {
		needed = needed || b.mapPipelineHasItemSlot(arm.source, arm.stages)
	}
	if !needed {
		return expression{}, false, nil
	}

	inputs := make([]expression, len(arms))
	streams := make([]jsonPipelineRows, len(arms))
	var stored []hff.Type
	var definitions, unions, nulls []string
	for i, arm := range arms {
		if _, err := b.jsonMapPipelineFrom(n, name, arm.source, arm.stages, &streams[i]); err != nil {
			return expression{}, true, err
		}
	}
	for i := range streams {
		input := &streams[i]
		if input.finish != nil {
			if err := input.finish(); err != nil {
				return expression{}, true, err
			}
		}
		rows := b.nextAlias()
		if input.query == "" {
			carrier := "NULL::boolean"
			if name == "map" {
				carrier = "NULL::json"
			}
			input.query = "SELECT 0::bigint AS ord," + carrier + " AS v,TRUE AS p,TRUE AS ip,TRUE AS keep WHERE FALSE"
		} else {
			stored = append(stored, input.stored)
		}
		definitions = append(definitions, rows+" AS MATERIALIZED ("+input.query+")")
		unions = append(unions, fmt.Sprintf("SELECT %d AS segment,ord,v,p,keep FROM %s", i, rows))
		inputs[i] = input.value
		inputs[i].present = both(input.value.present, "("+input.value.code+" IS NULL OR (SELECT COALESCE(bool_and(ip),TRUE) FROM "+rows+"))")
		nulls = append(nulls, "("+input.value.code+" IS NULL)")
	}
	inputNull := "(" + strings.Join(nulls, " OR ") + ")"
	joined, output := b.nextAlias(), b.nextAlias()
	definitions = append(definitions, joined+" AS MATERIALIZED ("+strings.Join(unions, " UNION ALL ")+")")
	terminalPresent := "(SELECT COALESCE(bool_and(p),TRUE) FROM " + joined + ")"
	present := both(ordinaryPresence(inputs), "("+inputNull+" OR "+terminalPresent+")")
	var code string
	kind := hff.BoolType
	switch name {
	case "map":
		kind = hff.ListType
		if b.jsonItemsRoles(n) == nil {
			for i, left := range stored {
				for _, right := range stored[i+1:] {
					if jsonTaggedStringStateAmbiguous(left, right) {
						return expression{}, true, b.failure(n, "concat pipeline map results require recoverable element types")
					}
				}
			}
		}
		order := " ORDER BY segment,ord"
		if b.orderBlind[n] {
			order = ""
		}
		code = "(SELECT COALESCE(json_agg(v" + order + "),'[]'::json) FROM " + joined + " WHERE keep)"
	case "any":
		code = "(SELECT COALESCE(bool_or(v),FALSE) FROM " + joined + " WHERE keep)"
	case "all":
		code = "(SELECT COALESCE(bool_and(v),TRUE) FROM " + joined + " WHERE keep)"
	case "count":
		kind = hff.IntType
		code = "(SELECT count(*) FROM " + joined + " WHERE keep AND v)"
	default:
		return expression{}, true, b.failure(n, "unsupported concat pipeline consumer")
	}
	code = "(CASE WHEN " + inputNull + " THEN NULL ELSE " + code + " END)"
	b.steps = append(b.steps, "(WITH "+strings.Join(definitions, ",")+" SELECT "+code+" AS v,"+present+" AS p OFFSET 0) AS "+output)
	if name == "map" {
		b.jsonValues[output+".v"] = JSON
		b.rememberNormalizedJSON(output+".v", JSON, hff.NewListType(hff.NewUnionType(stored...)))
	}
	return expression{output + ".v", output + ".p", kind}, true, nil
}

func listConcatNode(n hff.Node) bool {
	method, ok := n.Method()
	if !ok || method.Name() != "concat" || n.ChildCount() != 2 {
		return false
	}
	for _, signature := range method.Signatures() {
		if signature.ID() != "list.concat" {
			return false
		}
	}
	return true
}
