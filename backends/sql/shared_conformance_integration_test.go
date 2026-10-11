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
		"empty_filter": true, "and_before_or": true, "grouped_or": true,
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
	for _, tc := range conformance.Cases() {
		if !supported[tc.Name] {
			continue
		}
		delete(supported, tc.Name)
		e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, tc.Source)
		if err != nil {
			t.Fatal(tc.Name, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
			hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
			hffsql.Bind(hff.Path{"flag"}, hffsql.Column("records", "flag")),
		))
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
			expected = append(expected, expectation{tc.Name + "/" + row.Name, row.Match})
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
				values = append(values, sqlFixtureValue(t, v, []string{"bigint", "text", "boolean"}[i]))
			}
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s)) AS records(age,name,flag); EXECUTE %s%s; DEALLOCATE %s;\n", name, parameters, q.Where(), strings.Join(values, ","), name, execution, name)
		}
	}
	if len(supported) > 0 {
		t.Fatalf("shared cases disappeared: %v", supported)
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
