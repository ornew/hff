//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLFoldedNullRetainsExtensionCarrier(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, d := foldedNullExtension(t)
	var script strings.Builder
	for _, tc := range foldedNullCases {
		q := foldedNullQuery(t, e, d, tc.source, tc.carrier)
		fmt.Fprintf(&script, "SELECT %s FROM (VALUES ('x'::text)) records(name);\n", q.Where())
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(foldedNullCases) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(foldedNullCases), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s lost its %s SQL carrier: %s", foldedNullCases[i].source, foldedNullCases[i].carrier, result)
		}
	}
}
