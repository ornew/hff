//go:build integration

package sql_test

import "testing"

func TestPostgreSQLJSONPostConcatPipelinesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemPostConcatPipelineSources, itemMapPipelineFixtures()...)
}
