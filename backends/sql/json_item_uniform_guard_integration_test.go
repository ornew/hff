//go:build integration

package sql_test

import "testing"

func TestPostgreSQLUniformItemGuardsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, uniformItemGuardSources, unconstrainedItemGuardFixtures()...)
}
