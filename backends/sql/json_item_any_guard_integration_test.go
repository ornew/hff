//go:build integration

package sql_test

import "testing"

func TestPostgreSQLUnconstrainedItemGuardsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, unconstrainedItemGuardSources, unconstrainedItemGuardFixtures()...)
}
