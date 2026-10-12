//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLCanonicalSerializationGuards(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("set HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	values := []hff.Value{hff.Null(), hff.Bool(false), hff.Int(1), hff.Float(math.Copysign(0, -1)), hff.String("Tokyo"), hff.List(hff.Int(1), hff.Null()), hff.List(hff.Null()), hff.Object(map[string]hff.Value{"x": hff.Int(1)})}
	sources := []string{`value!:json=expected`, `value:[number]:json=expected`, `value:[list]:json=expected`, `value![0]!:json=expected`}
	var script strings.Builder
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		e := serializationEngine(t, serializationWorkload{typ: schema.AnyType})
		for si, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			f, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(source, err)
			}
			q, err := hffsql.Build(ctx, p, serializationOptions(format)...)
			if err != nil {
				t.Fatal(source, err)
			}
			var rows []string
			for _, v := range values {
				result := v
				want := false
				switch si {
				case 0:
					want = v.Kind() != hff.NullKind
				case 1:
					want = v.Kind() == hff.IntKind || v.Kind() == hff.FloatKind
				case 2:
					want = v.Kind() == hff.ListKind
				case 3:
					result = hff.Null()
					if list, ok := v.AsList(); ok && len(list) > 0 {
						result = list[0]
						want = result.Kind() != hff.NullKind
					}
				}
				expected := metadataJSONFixture(t, result)
				got, err := f.Match(ctx, hff.Object(map[string]hff.Value{"value": v, "expected": hff.String(expected)}))
				if err != nil || got != want {
					t.Fatalf("local %s: got%t want%t: %v", source, got, want, err)
				}
				rows = append(rows, fmt.Sprintf("(%s::%s,%s::jsonb,%s::text,%t)", sqlFixtureString(metadataJSONFixture(t, v)), format, sqlFixtureString(pairTypeMetadata(t, v)), sqlFixtureString(expected), want))
			}
			var types, args []string
			for _, arg := range q.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, literal)
			}
			params, arguments := "", ""
			if len(types) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE guarded_json_%d_%d%s AS SELECT (%s)=want FROM (VALUES%s) records(value,metadata,expected,want); EXECUTE guarded_json_%d_%d%s;\n", fi, si, params, q.Where(), strings.Join(rows, ","), fi, si, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != 64 {
		t.Fatalf("got%d results want64: %s", len(results), out)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("case%d: %s", i, result)
		}
	}
}
