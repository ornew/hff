//go:build integration

package sql_test

import "testing"

func TestPostgreSQLDeferredRowSelectionsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredSelectionSources, deferredSelectionFixtures()...)
}
