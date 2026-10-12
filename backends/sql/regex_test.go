package sql_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
)

func TestPostgreSQLRegexSubsetIsExplicit(t *testing.T) {
	ctx := context.Background()
	for _, pattern := range []string{".", "(?i)A", "(?m)^A", "日", "[^a]", `\bA`, "a{256}", "(?:^A)*", `\x00`} {
		quoted, _ := json.Marshal(pattern)
		p := compile(t, "name~"+string(quoted))
		_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.BackendError || detail.Span == nil {
			t.Fatalf("%q accepted unsupported pattern: %v", pattern, err)
		}
	}
	_, err := hffsql.Build(ctx, compile(t, "name~name"), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	requireCode(t, err, hff.BackendError)
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []capabilities.ID{capabilities.RegexOperators, capabilities.Method("string.matches")} {
		feature, ok := profile.Feature(id)
		if !ok || feature.Level != hff.Conditional {
			t.Fatalf("%s: %+v", id, feature)
		}
	}
}
