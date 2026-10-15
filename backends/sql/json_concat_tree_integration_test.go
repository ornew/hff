//go:build integration

package sql_test

import "testing"

func TestPostgreSQLJSONConcatTreesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemConcatTreeSources, itemMapPipelineFixtures()...)
}
