//go:build integration

package sql_test

import "testing"

func TestPostgreSQLRuntimeSortMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeSortSources, runtimeSortFixtures()...)
}

func TestPostgreSQLRuntimeSortAnyMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeSortAnySources, append(itemAnyFixtures(), runtimeSortEdgeFixture())...)
}
