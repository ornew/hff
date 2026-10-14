//go:build integration

package sql

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
)

func TestPostgreSQLJSONNumericFamilyCacheIdentity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var script strings.Builder
	for _, tc := range []struct {
		text        string
		left, right jsonNumericSide
		lf, rf      string
		want        bool
	}{
		{"'1'::text", jsonNumericSide{true, true, true}, jsonNumericSide{false, true, false}, "", "", true},
		{"'1.0'::text", jsonNumericSide{true, true, true}, jsonNumericSide{true, true, true}, "1.0::double precision", "2.0::double precision", false},
		{"'1.0'::text", jsonNumericSide{true, true, true}, jsonNumericSide{true, true, true}, "2.0::double precision", "", false},
	} {
		b := &builder{ctx: ctx}
		code := b.decodedJSONNumericPair(tc.text, tc.text, hff.Equal, tc.left, tc.right, tc.lf, tc.rf)
		fmt.Fprintf(&script, "SELECT (%s) IS NOT DISTINCT FROM %t;\n", code, tc.want)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Join(strings.Fields(string(out)), ",") != "t,t,t" {
		t.Fatal("family guards or predecoded Float identity were conflated", string(out))
	}
}
