//go:build integration

package sql_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
)

// Keep an explicit supported slice: unsupported cases must not silently vanish
// when a translator regresses or new language examples are added.
func TestPostgreSQLSharedScalarConformance(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	supported := map[string]bool{
		"nonnull_guards_preserve_zero": true, "nonnull_null_test_is_false": true,
		"nullable_guards_keep_presence": true,
		"empty_filter":                  true, "and_before_or": true, "grouped_or": true,
		"implicit_truthiness": true, "fractional_integer_division": true,
		"overflow_is_null": true, "regex_partial_match": true,
		"invalid_regex_does_not_match_negation": true, "unicode_code_points": true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	script.WriteString("SET standard_conforming_strings=on;\n")
	type expectation struct {
		name  string
		match bool
	}
	var expected []expectation
	seen := map[string]bool{}
	for _, storage := range []string{"native", "json", "jsonb"} {
		for _, tc := range conformance.Cases() {
			if !supported[tc.Name] {
				continue
			}
			seen[tc.Name] = true
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities), engine.WithLimits(tc.Limits))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(ctx, tc.Source)
			if err != nil {
				t.Fatal(tc.Name, err)
			}
			var bindings []hffsql.Binding
			for _, field := range []string{"age", "name", "flag"} {
				column := hffsql.Column("records", field)
				binding := hffsql.Bind(hff.Path{field}, column)
				if storage != "native" {
					format := hffsql.JSON
					if storage == "jsonb" {
						format = hffsql.JSONB
					}
					binding = hffsql.BindJSON(hff.Path{field}, column, format)
				}
				bindings = append(bindings, binding)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if tc.Name == "regex_partial_match" || tc.Name == "invalid_regex_does_not_match_negation" {
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Code != hff.BackendError {
					t.Fatalf("%s: expected unsupported regex, got %v", tc.Name, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(tc.Name, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range tc.Records {
				match, err := filter.Match(ctx, row.Value)
				if err != nil || match != row.Match {
					t.Fatalf("%s/%s local=(%v,%v), want %v", tc.Name, row.Name, match, err, row.Match)
				}
				name := fmt.Sprintf("shared_%d", len(expected))
				expected = append(expected, expectation{storage + "/" + tc.Name + "/" + row.Name, row.Match})
				var types, literals []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					literals = append(literals, literal)
				}
				parameters, execution := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					execution = "(" + strings.Join(literals, ",") + ")"
				}
				var values []string
				for i, field := range []string{"age", "name", "flag"} {
					v, _ := row.Value.Lookup(field)
					literal := sqlFixtureValue(t, v, []string{"bigint", "text", "boolean"}[i])
					if storage != "native" {
						literal = "to_json(" + literal + ")::" + storage
					}
					values = append(values, literal)
				}
				fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s)) AS records(age,name,flag); EXECUTE %s%s; DEALLOCATE %s;\n", name, parameters, q.Where(), strings.Join(values, ","), name, execution, name)
			}
		}
	}
	for name := range supported {
		if !seen[name] {
			t.Fatalf("shared case disappeared: %s", name)
		}
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL shared conformance: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		value := "f"
		if want.match {
			value = "t"
		}
		if actual[i] != value {
			t.Errorf("%s: PostgreSQL=%s want=%s", want.name, actual[i], value)
		}
	}
	t.Logf("verified %d shared query/record pairs", len(expected))
}
