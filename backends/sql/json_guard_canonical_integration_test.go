//go:build integration

package sql_test

import "testing"

func TestPostgreSQLImpossibleItemGuardsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, impossibleItemGuardSources, unconstrainedItemGuardFixtures()...)
}
