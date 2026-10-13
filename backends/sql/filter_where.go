package sql

import (
	"strings"

	"github.com/ornew/hff"
)

type filterCoverage struct{ all, any bool }

// Split only the filter's logical skeleton. Value arguments and callback
// bodies keep ordinary lowering, with their existing presence and scope rules.
func (b *builder) filterWhere(n hff.Node) (string, error) {
	coverage := make(map[hff.Node]filterCoverage)
	if _, err := b.classifyFilter(n, coverage); err != nil {
		return "", err
	}
	if coverage[n].any && !coverage[n].all {
		paths := make(map[string]int)
		scope := 0
		shared, err := b.overlappingFilterScopes(n, coverage, paths, &scope)
		if err != nil {
			return "", err
		}
		if shared {
			// Separate scalar subqueries cannot share their aliases. Preserve
			// the original scope rather than duplicating a cached input/kernel.
			return b.generalFilterWhere(n)
		}
	}
	return b.composeFilterWhere(n, coverage)
}

func (b *builder) classifyFilter(n hff.Node, coverage map[hff.Node]filterCoverage) (filterCoverage, error) {
	if err := checkContext(b.ctx); err != nil {
		return filterCoverage{}, err
	}
	result := filterCoverage{all: true}
	switch n.Kind() {
	case hff.NotNode, hff.AndNode, hff.OrNode:
		result.all = n.ChildCount() != 0
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			part, err := b.classifyFilter(child, coverage)
			if err != nil {
				return filterCoverage{}, err
			}
			result.all = result.all && part.all
			result.any = result.any || part.any
		}
	default:
		eligible := b.directScalarWhereEligible(n)
		result = filterCoverage{all: eligible, any: eligible}
	}
	coverage[n] = result
	return result, nil
}

// Every mixed AND/OR owns one shared scope for all entirely generic siblings,
// including siblings separated by direct predicates. Nested mixed operators
// own other scopes; overlapping schema paths conservatively disable splitting.
func (b *builder) overlappingFilterScopes(n hff.Node, coverage map[hff.Node]filterCoverage, paths map[string]int, scope *int) (bool, error) {
	if coverage[n].all {
		return false, nil
	}
	if !coverage[n].any {
		*scope += 1
		return b.filterScopePaths(n, paths, *scope)
	}
	if n.Kind() == hff.NotNode {
		child, _ := n.Child(0)
		return b.overlappingFilterScopes(child, coverage, paths, scope)
	}
	*scope += 1
	group := *scope
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		var shared bool
		var err error
		if !coverage[child].any {
			shared, err = b.filterScopePaths(child, paths, group)
		} else {
			shared, err = b.overlappingFilterScopes(child, coverage, paths, scope)
		}
		if shared || err != nil {
			return shared, err
		}
	}
	return false, nil
}

func (b *builder) filterScopePaths(n hff.Node, paths map[string]int, scope int) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if n.Kind() == hff.VariableNode || n.Kind() == hff.FieldNode {
		root := n
		for root.Kind() == hff.FieldNode {
			root, _ = root.Child(0)
		}
		if path, ok := n.Path(); ok && root.Kind() == hff.VariableNode {
			key := pathKey(path)
			if previous, found := paths[key]; found && previous != scope {
				return true, nil
			}
			paths[key] = scope
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		shared, err := b.filterScopePaths(child, paths, scope)
		if shared || err != nil {
			return shared, err
		}
	}
	return false, nil
}

func (b *builder) composeFilterWhere(n hff.Node, coverage map[hff.Node]filterCoverage) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if coverage[n].all {
		return b.directScalarWhere(n)
	}
	if !coverage[n].any {
		return b.generalFilterWhere(n)
	}
	if n.Kind() == hff.NotNode {
		child, _ := n.Child(0)
		code, err := b.composeFilterWhere(child, coverage)
		return "(NOT " + code + ")", err
	}
	outerSteps, outerPaths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outerSteps, outerPaths }()
	var parts, generic []string
	groupPosition := -1
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if !coverage[child].any {
			value, err := b.expression(child)
			if err != nil {
				return "", err
			}
			generic = append(generic, b.truth(child, value))
			if groupPosition < 0 {
				groupPosition = len(parts)
				parts = append(parts, "")
			}
			continue
		}
		// Lower immediately, retaining source order for arguments and custom
		// translators even though the generic siblings share one SQL group.
		part, err := b.composeFilterWhere(child, coverage)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	join := " AND "
	if n.Kind() == hff.OrNode {
		join = " OR "
	}
	if groupPosition >= 0 {
		parts[groupPosition] = b.filterProjection("(" + strings.Join(generic, join) + ")")
	}
	return "(" + strings.Join(parts, join) + ")", nil
}

func (b *builder) generalFilterWhere(n hff.Node) (string, error) {
	outerSteps, outerPaths := b.steps, b.sharedPaths
	b.steps, b.sharedPaths = nil, nil
	defer func() { b.steps, b.sharedPaths = outerSteps, outerPaths }()
	value, err := b.expression(n)
	if err != nil {
		return "", err
	}
	return b.filterProjection(b.truth(n, value)), nil
}

func (b *builder) filterProjection(predicate string) string {
	if len(b.steps) == 0 {
		return predicate
	}
	return "(SELECT " + predicate + " FROM " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
}
