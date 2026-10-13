//go:build integration

package sql_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

// These tests use psql only as a test transport. The backend has no dependency
// on Docker, psql, a database driver, or a running database.
func TestPostgreSQLScalarParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e := sqlEngine(t)
	var script strings.Builder
	script.WriteString("SET standard_conforming_strings=on; SET extra_float_digits=-3;\n")
	var expected []string
	for i, tc := range parityCases() {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"age": tc.age, "name": tc.name, "flag": tc.flag, "factor": tc.factor}))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		want := "f"
		if match {
			want = "t"
		}
		expected = append(expected, want)
		name := fmt.Sprintf("case_%d", i)
		args := query.Args()
		types := make([]string, len(args))
		literals := make([]string, len(args))
		for j, arg := range args {
			types[j], literals[j] = sqlFixtureParameter(t, arg)
		}
		parameters := ""
		execution := ""
		if len(args) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			execution = "(" + strings.Join(literals, ",") + ")"
		}
		row := []string{sqlFixtureValue(t, tc.age, "bigint"), sqlFixtureValue(t, tc.name, "text"), sqlFixtureValue(t, tc.flag, "boolean"), sqlFixtureValue(t, tc.factor, "double precision")}
		fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s)) AS records(age,name,flag,factor);\nEXECUTE %s%s;\nDEALLOCATE %s;\n", name, parameters, query.Where(), strings.Join(row, ","), name, execution, name)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL conformance failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results; want %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			tc := parityCases()[i]
			t.Errorf("query %q row age=%v name=%v flag=%v factor=%v: PostgreSQL=%s local=%s", tc.source, tc.age, tc.name, tc.flag, tc.factor, actual[i], want)
		}
	}
	t.Logf("verified %d scalar query/record pairs against PostgreSQL", len(expected))
}

// Fixture literals are confined to test input. Production query literals are
// always parameters; these helpers are not a replacement for driver binding.
func sqlFixtureParameter(t testing.TB, value any) (string, string) {
	t.Helper()
	switch value := value.(type) {
	case int64:
		return "bigint", strconv.FormatInt(value, 10)
	case float64:
		return "double precision", sqlFixtureString(strconv.FormatFloat(value, 'g', -1, 64))
	case bool:
		return "boolean", strconv.FormatBool(value)
	case string:
		return "text", sqlFixtureString(value)
	default:
		t.Fatalf("unsupported fixture parameter %T", value)
		return "", ""
	}
}

func sqlFixtureString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func sqlFixtureValue(t testing.TB, value hff.Value, typ string) string {
	t.Helper()
	if value.Kind() == hff.NullKind {
		return "NULL::" + typ
	}
	if value.Kind() == hff.BytesKind {
		data, _ := value.AsBytes()
		return "decode(" + sqlFixtureString(hex.EncodeToString(data)) + ",'hex')"
	}
	var arg any
	switch value.Kind() {
	case hff.IntKind:
		arg, _ = value.AsInt()
	case hff.DurationKind:
		duration, _ := value.AsDuration()
		arg = int64(duration)
	case hff.FloatKind:
		arg, _ = value.AsFloat()
	case hff.StringKind:
		arg, _ = value.AsString()
	case hff.BoolKind:
		arg, _ = value.AsBool()
	default:
		t.Fatal("invalid scalar fixture")
	}
	_, literal := sqlFixtureParameter(t, arg)
	if value.Kind() == hff.IntKind || value.Kind() == hff.DurationKind {
		// Cast the signed text as a whole; casting the positive magnitude of
		// MinInt64 before unary minus would overflow inside PostgreSQL.
		literal = sqlFixtureString(literal)
	}
	return "(" + literal + "::" + typ + ")"
}

func TestPostgreSQLExtensionParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ds := extensionDeclarations(t)
	e := extensionEngine(t, ds)
	sources := []string{
		`name:customprefix("Al")`, `name:customprefix("Al"):not`,
		`!name!:customprefix("Al")`, `name!:customprefix("Al")=null`,
		`name:customprefix(null)=null`, `name:nullseen`, `name!:nullseen`,
		`name:[int]:nullseen`, `name:[string]:customprefix("Al")`,
		`name:customprefix("O'Reilly")`, `name:customprefix("")`,
		`name:customprefix("é")`, `name:customprefix("a%b")`,
		`name:customprefix("Al")|name:nullseen`, `name:nullseen name:customprefix("Al")`,
		`name:nullarg(null)`, `name:nullarg(name!)=null`,
		`name:allpresent(null,name!)=null`, `name:allpresent("",name!)`,
	}
	rows := []hff.Value{hff.Null(), hff.String(""), hff.String("Alice"), hff.String("Al"), hff.String("Bob"), hff.String("O'Reilly"), hff.String("éclair"), hff.String("a%b suffix")}
	var script strings.Builder
	var expected []string
	script.WriteString("SET standard_conforming_strings=on;\n")
	for _, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, extensionOptions(ds)...)
		if err != nil {
			t.Fatal(source, err)
		}
		f, err := local.Build(ctx, p, localExtensionOptions(ds)...)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			matched, err := f.Match(ctx, hff.Object(map[string]hff.Value{"name": row}))
			if err != nil {
				t.Fatal(err)
			}
			want := "f"
			if matched {
				want = "t"
			}
			expected = append(expected, want)
			args := q.Args()
			types := make([]string, len(args))
			literals := make([]string, len(args))
			for i, a := range args {
				types[i], literals[i] = sqlFixtureParameter(t, a)
			}
			params, execution := "", ""
			if len(args) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				execution = "(" + strings.Join(literals, ",") + ")"
			}
			name := fmt.Sprintf("extension_%d", len(expected))
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s)) AS records(name);\nEXECUTE %s%s;\nDEALLOCATE %s;\n", name, params, q.Where(), sqlFixtureValue(t, row, "text"), name, execution, name)
		}
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("extension parity failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results; want %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("%q row %v: PostgreSQL=%s local=%s", sources[i/len(rows)], rows[i%len(rows)], actual[i], want)
		}
	}
	t.Logf("verified %d extension query/record pairs against PostgreSQL", len(expected))
}
