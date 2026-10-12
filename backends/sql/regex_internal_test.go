package sql

import (
	"regexp/syntax"
	"testing"
)

func TestPostgresRegexEmptyClass(t *testing.T) {
	for _, pattern := range []string{`[^\s\S]`, `[^\d\D]`} {
		parsed, err := syntax.Parse(pattern, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		got, err := postgresRegex(parsed)
		if err != nil || got != "(?!)" {
			t.Fatalf("%s -> %q, %v", pattern, got, err)
		}
	}
}
