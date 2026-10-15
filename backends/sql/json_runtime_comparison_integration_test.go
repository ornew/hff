//go:build integration

package sql_test

import "testing"

func TestPostgreSQLRuntimeComparisonsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeComparisonSources, deferredValueFixtures()...)
}

func TestPostgreSQLRuntimeScalarComparisonsMatchLocal(t *testing.T) {
	fixtures := append(itemAnyFixtures(), runtimeComparisonEdgeFixture())
	runPostgreSQLJSONItemRoleSources(t, runtimeScalarComparisonSources, fixtures...)
}
