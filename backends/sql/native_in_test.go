package sql_test

import (
	"context"
	"strings"
	"testing"

	hffsql "github.com/ornew/hff/backends/sql"
)

func TestNativeScalarInAvoidsJSONRoundTrips(t *testing.T) {
	for _, source := range []string{
		`age:in(18,21)`,
		`name:in("Alice","Bob",null)`,
		`flag:in(true,false)`,
		`name:in(name!)`,
	} {
		query, err := hffsql.Build(context.Background(), compile(t, source),
			hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(source, err)
		}
		where := query.Where()
		if strings.Contains(where, "to_json(") || strings.Contains(where, "::json") ||
			strings.Contains(where, "json_typeof(") {
			t.Fatalf("%s: scalar membership contains a JSON round trip: %s", source, where)
		}
		if source != `name:in(name!)` && !strings.Contains(where, " IN (") {
			t.Fatalf("%s: native membership is missing: %s", source, where)
		}
		if source != `name:in(name!)` && strings.Contains(where, "SELECT") {
			t.Fatalf("%s: literal membership retained projection barriers: %s", source, where)
		}
	}

	query, err := hffsql.Build(context.Background(), compile(t, `age:in(18,1.5)`),
		hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where(), "::json") {
		t.Fatal("mixed numeric membership must retain exact cross-subtype equality:", query.Where())
	}
}
