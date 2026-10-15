package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Each concat keeps its own input state even when its rows stay in their
// original item-slot scopes. Maps above that concat must not expose failures
// when one of its operands was null; maps below it may already have failed.
type jsonConcatPipelineTree struct {
	source      hff.Node
	stages      []hff.Node
	children    []*jsonConcatPipelineTree
	leaves      []int
	checkpoints []string
}

func jsonConcatTree(n hff.Node) (*jsonConcatPipelineTree, bool) {
	source, stages := mapPipelineStages(n)
	tree := &jsonConcatPipelineTree{source: source, stages: stages}
	nested := false
	if listConcatNode(source) {
		nested = len(stages) > 0
		for i := 0; i < source.ChildCount(); i++ {
			child, _ := source.Child(i)
			subtree, inner := jsonConcatTree(child)
			tree.children = append(tree.children, subtree)
			nested = nested || inner
		}
	}
	return tree, nested
}

func (t *jsonConcatPipelineTree) arms(after []hff.Node) []jsonPipelineArm {
	var arms []jsonPipelineArm
	var visit func(*jsonConcatPipelineTree, []hff.Node)
	visit = func(node *jsonConcatPipelineTree, after []hff.Node) {
		stages := append(append([]hff.Node(nil), node.stages...), after...)
		start := len(arms)
		if len(node.children) == 0 {
			arms = append(arms, jsonPipelineArm{node.source, stages})
		} else {
			for _, child := range node.children {
				visit(child, stages)
			}
		}
		for i := start; i < len(arms); i++ {
			node.leaves = append(node.leaves, i)
		}
	}
	visit(t, after)
	return arms
}

// Follow expression order rather than global depth: a left subtree's maps
// finish before the right source is prepared, and parent stages then advance
// all their arms together. Every continuation restores its lexical SQL scope.
func (t *jsonConcatPipelineTree) prepare(b *builder, n hff.Node, name string, arms []jsonPipelineArm, streams []jsonPipelineRows) error {
	if len(t.children) == 0 {
		i := t.leaves[0]
		streams[i].tracked = true
		if _, err := b.jsonMapPipelineFrom(n, name, arms[i].source, arms[i].stages, &streams[i]); err != nil {
			return err
		}
	} else {
		for _, child := range t.children {
			if err := child.prepare(b, n, name, arms, streams); err != nil {
				return err
			}
		}
	}
	mapped := false
	for _, stage := range t.stages {
		method, _ := stage.Method()
		mapped = mapped || method.Name() == "map"
		for _, i := range t.leaves {
			if streams[i].advance != nil {
				if err := streams[i].advance(); err != nil {
					return err
				}
			}
		}
	}
	if mapped {
		for _, i := range t.leaves {
			column := ""
			if streams[i].checkpoint != nil {
				column = streams[i].checkpoint()
			}
			t.checkpoints = append(t.checkpoints, column)
		}
	}
	return nil
}

type jsonConcatPipelineState struct{ null, present string }

// Materialize scalar states once, so ancestors reference short names rather
// than expanding the same descendant conditions in every checkpoint scan.
func (t *jsonConcatPipelineTree) state(b *builder, streams []jsonPipelineRows, rows []string, definitions *[]string) jsonConcatPipelineState {
	var state jsonConcatPipelineState
	if len(t.children) == 0 {
		value := streams[t.leaves[0]].value
		state = jsonConcatPipelineState{"(" + value.code + " IS NULL)", value.present}
	} else {
		left := t.children[0].state(b, streams, rows, definitions)
		right := t.children[1].state(b, streams, rows, definitions)
		state.null = "(" + left.null + " OR " + right.null + ")"
		state.present = both(left.present, "("+left.null+" OR "+right.present+")")
	}
	if len(t.checkpoints) > 0 {
		var valid []string
		for j, i := range t.leaves {
			if column := t.checkpoints[j]; column != "" && rows[i] != "" {
				valid = append(valid, "(SELECT COALESCE(bool_and(COALESCE("+column+",FALSE)),TRUE) FROM "+rows[i]+")")
			}
		}
		if len(valid) > 0 {
			state.present = both(state.present, "("+state.null+" OR ("+strings.Join(valid, " AND ")+"))")
		}
	}
	alias := b.nextAlias()
	*definitions = append(*definitions, alias+" AS MATERIALIZED (SELECT "+state.null+" AS n,"+state.present+" AS p)")
	return jsonConcatPipelineState{"(SELECT n FROM " + alias + ")", "(SELECT p FROM " + alias + ")"}
}
