package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
)

func TestComputedJSONFieldsFromScalarBindings(t *testing.T) {
	for _, source := range []string{`name:fromjson:[object].a:exists`, `({"a":name}).a=name`, `[{"a":name}][0].a=name`} {
		_, err := hffsql.Build(context.Background(), compile(t, source), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	feature, ok := profile.Feature(capabilities.Method("string.fromjson"))
	if !ok || feature.Level != hff.Conditional || !strings.Contains(feature.Conditions, "PostgreSQL 16+") {
		t.Fatalf("parse profile: %+v", feature)
	}
}
