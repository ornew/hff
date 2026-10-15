//go:build integration

package sql_test

import "testing"

func TestPostgreSQLGuardedMapJSONMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, guardedMapJSONSources, guardedMapJSONFixtures()...)
}
