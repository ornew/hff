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

func TestPostgreSQLJSONNormalizationFixedPoint(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw := []string{`null`, `0`, `1`, `1.0`, `9007199254740993`, `9223372036854775807`, `-9223372036854775808`, `-0.0`, `5e-324`, `1e-400`, `true`, `"YQ=="`, `"東京"`, `[]`, `[1,1.0,null]`, `[[1.0],[]]`, `{}`, `{"f":1,"i":1.0,"extra":2}`, `{"f":1.0,"i":9007199254740993,"extra":null}`, `{"extra":[1.0],"f":"YQ=="}`}
	var rows []string
	for _, text := range raw {
		rows = append(rows, "('"+strings.ReplaceAll(text, "'", "''")+"'::json)")
	}
	var script strings.Builder
	var labels []string
	checks, pairs := 0, 0
	for _, source := range normalizationFixtureTypes() {
		for _, target := range normalizationFixtureTypes() {
			b := &builder{ctx: ctx}
			stable, err := b.normalizationPreservesJSON(source, target)
			if err != nil {
				t.Fatal(err)
			}
			if !stable {
				continue
			}
			left, err := b.comparisonDocument(hff.Node{}, "raw.v", JSON, source)
			if err != nil {
				t.Fatal(source.Key(), err)
			}
			right, err := b.comparisonDocument(hff.Node{}, "normalized.v", JSON, target)
			if err != nil {
				t.Fatal(target.Key(), err)
			}
			var types, args []string
			for _, arg := range b.args {
				key, ok := arg.(string)
				if !ok {
					t.Fatalf("unexpected normalization argument %T", arg)
				}
				types = append(types, "text")
				args = append(args, "'"+strings.ReplaceAll(key, "'", "''")+"'")
			}
			labels = append(labels, source.Key()+" -> "+target.Key())
			name := fmt.Sprintf("fixedpoint_%d", pairs)
			pairs++
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT normalized.v::text=(%s)::text FROM (VALUES %s) raw(v) CROSS JOIN LATERAL (SELECT %s AS v OFFSET 0) normalized;\n", name, parameters, right, strings.Join(rows, ","), left)
			for _, digits := range []int{1, 3} {
				fmt.Fprintf(&script, "SET extra_float_digits=%d; EXECUTE %s%s;\n", digits, name, arguments)
				checks += len(rows)
			}
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		completed := 0
		for _, line := range strings.Split(string(out), "\n") {
			if line == "t" || line == "f" {
				completed++
			}
		}
		pair := completed / (len(rows) * 2)
		detail := string(out)
		if len(detail) > 2000 {
			detail = detail[len(detail)-2000:]
		}
		t.Fatalf("%v at schema pair %d (%s)\n%s", err, pair, labels[pair], detail)
	}
	results := strings.Fields(string(out))
	if len(results) != checks {
		t.Fatalf("got %d results, want %d: %s", len(results), checks, out)
	}
	for index, result := range results {
		if result != "t" {
			t.Errorf("pair %d row %d: normalizing again changed represented JSON", index/(len(rows)*2), index%len(rows))
		}
	}
	t.Logf("checked %d normalization fixed points over %d schema pairs", checks, pairs)
}
