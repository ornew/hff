package sql

import (
	"fmt"
	"strings"

	"github.com/ornew/hff"
)

// Only row-dependent built-ins can move behind a row gate. Captured records,
// extensions and independent method subtrees retain their existing placement;
// moving them could repeat work that PostgreSQL currently hoists out of a loop.
func rowLocalPipelineCallback(callback hff.Node) bool {
	parameters, ok := callback.Parameters()
	if !ok || len(parameters) != 1 {
		return false
	}
	allowed := map[hff.BindingID]bool{parameters[0].ID(): true}
	work := false
	var visit func(hff.Node) (map[hff.BindingID]bool, bool)
	visit = func(n hff.Node) (map[hff.BindingID]bool, bool) {
		if n.Kind() == hff.VariableNode {
			return nil, false
		}
		if n.Kind() == hff.ParameterNode {
			binding, ok := n.Binding()
			if !ok || !allowed[binding.ID()] {
				return nil, false
			}
			return map[hff.BindingID]bool{binding.ID(): true}, true
		}
		if n.Kind() == hff.CallNode || n.Kind() == hff.TypeGuardNode {
			work = true
		}
		if method, ok := n.Method(); ok {
			for _, signature := range method.Signatures() {
				if _, custom := signature.Declaration(); custom {
					return nil, false
				}
			}
		}
		locals, _ := n.Parameters()
		for _, local := range locals {
			allowed[local.ID()] = true
		}
		defer func() {
			for _, local := range locals {
				delete(allowed, local.ID())
			}
		}()
		free := make(map[hff.BindingID]bool)
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			dependencies, safe := visit(child)
			if !safe {
				return nil, false
			}
			for id := range dependencies {
				free[id] = true
			}
		}
		for _, local := range locals {
			delete(free, local.ID())
		}
		if n.Kind() == hff.CallNode && len(free) == 0 {
			return nil, false
		}
		return free, true
	}
	body, _ := callback.Child(0)
	free, safe := visit(body)
	return safe && work && free[parameters[0].ID()]
}

type pipelineStageScope struct {
	selected   string
	steps      []string
	paths      map[string]expression
	parameters map[hff.BindingID]jsonParameter
	memo       *jsonItemRoleMemo
}

func (b *builder) beginPipelineStage(callback hff.Node, selected *string) *pipelineStageScope {
	if *selected == "TRUE" || !rowLocalPipelineCallback(callback) {
		return nil
	}
	*selected = b.project(*selected)
	scope := &pipelineStageScope{*selected, b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo}
	b.steps, b.sharedPaths, b.itemRoleMemo = nil, nil, nil
	b.parameters = make(map[hff.BindingID]jsonParameter, len(scope.parameters))
	for id, parameter := range scope.parameters {
		b.parameters[id] = parameter
	}
	return scope
}

// Keep a source row even when its stage is inactive: earlier map validity must
// still be aggregated over that row. The entry barrier precedes every body
// projection, and the body references its active column to retain correlation.
func (scope *pipelineStageScope) finish(b *builder, columns ...string) []string {
	seed, entry, calculation, gated, output := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	selects, references := make([]string, len(columns)), make([]string, len(columns))
	for i, column := range columns {
		name := fmt.Sprintf("v%d", i)
		selects[i], references[i] = column+" AS "+name, output+"."+name
	}
	from := "(VALUES (1)) AS " + seed + "(n)"
	if len(b.steps) > 0 {
		from += " CROSS JOIN LATERAL " + strings.Join(b.steps, " CROSS JOIN LATERAL ")
	}
	body := "SELECT " + strings.Join(selects, ",") + " FROM " + from + " WHERE " + entry + ".active OFFSET 0"
	gate := "SELECT " + calculation + ".* FROM (SELECT " + scope.selected + " AS active WHERE " + scope.selected + " OFFSET 0) AS " + entry + " CROSS JOIN LATERAL (" + body + ") AS " + calculation + " OFFSET 0"
	step := "(SELECT " + gated + ".* FROM (VALUES (1)) AS " + seed + "(n) LEFT JOIN LATERAL (" + gate + ") AS " + gated + " ON TRUE OFFSET 0) AS " + output
	b.steps, b.sharedPaths, b.parameters, b.itemRoleMemo = append(scope.steps, step), scope.paths, scope.parameters, scope.memo
	return references
}

func (scope *pipelineStageScope) exportSlot(b *builder, slot jsonItemSlot) jsonItemSlot {
	oldRaw, oldPair := slot.raw, slot.pair
	values := scope.finish(b, slot.raw, slot.pair, slot.key, slot.value, slot.present)
	slot.raw, slot.pair, slot.present = values[0], values[1], values[4]
	if slot.key != "TRUE" && slot.key != "FALSE" {
		slot.key = values[2]
	}
	if slot.value != "TRUE" && slot.value != "FALSE" {
		slot.value = values[3]
	}
	b.copyNormalizedJSON(oldRaw, slot.raw, slot.format)
	if format := b.jsonValues[oldPair]; format != "" {
		b.jsonValues[slot.pair] = format
		b.copyNormalizedJSON(oldPair, slot.pair, format)
	}
	return slot
}

func (scope *pipelineStageScope) exportValue(b *builder, value expression) expression {
	if value.kind == hff.NullType {
		// Projecting an untyped NULL would resolve its carrier as SQL text.
		// Preserve the same constant carrier as ordinary expression lowering.
		value.present = scope.finish(b, value.present)[0]
		return value
	}
	old := value.code
	values := scope.finish(b, value.code, value.present)
	value.code, value.present = values[0], values[1]
	if format := b.jsonValues[old]; format != "" {
		b.jsonValues[value.code] = format
		b.copyNormalizedJSON(old, value.code, format)
	}
	return value
}
