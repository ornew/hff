//go:build integration

package sql_test

import "testing"

func TestPostgreSQLRuntimeMembershipMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeMembershipSources, deferredValueFixtures()...)
}

func TestPostgreSQLRuntimeMembershipAnyMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeMembershipAnySources, append(itemAnyFixtures(), runtimeMembershipEdgeFixture())...)
}

func TestPostgreSQLRuntimeDistinctPairRolesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, runtimeDistinctPairSources, itemRoleFixtures()...)
}
