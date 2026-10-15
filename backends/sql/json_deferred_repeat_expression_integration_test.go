//go:build integration

package sql_test

import "testing"

func TestPostgreSQLRepeatedJSONExpressionsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, acceptedRepeatedExpressionSources, flatmapPipelineFixtures()...)
}
