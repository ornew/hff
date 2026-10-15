//go:build integration

package sql_test

import "testing"

func TestPostgreSQLDeferredInputFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredInputFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLDeferredInputFoldWideObject(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		if tc.name == "bytes/mixed" {
			runPostgreSQLJSONItemRoleSources(t, func(itemRoleFixture) []string { return []string{deferredWidePacketSource()} }, tc)
			return
		}
	}
	t.Fatal("missing mixed Bytes fixture")
}

func TestPostgreSQLDeferredZeroInputFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredZeroInputFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLDeferredFoldAccumulatorRoles(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		if tc.name == "bytes/any-field" {
			runPostgreSQLJSONItemRoleSources(t, deferredFoldAccumulatorRoleSources, tc)
			return
		}
	}
	t.Fatal("missing Any/Bytes fixture")
}
