package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestNormalizedFoldOutputAvoidsRepeatedTraversal(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("items", schema.NullableType(schema.ListOf(schema.NullableType(schema.FloatType)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		arrays int
	}{
		{`[items:foldl([], (a,x)=>a:concat([x]))]:size>0`, 5},
		{`[items:foldr([], (x,a)=>[x]:concat(a))]:size>0`, 5},
		{`[items:foldl([], (a,x)=>items:foldl(a,(a,y)=>a:concat([y])))]:size>0`, 6},
		{`items:foldl([], (a,x)=>a:concat([x])):size>0`, 5},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(q.Where(), "json_array_elements("); got != tc.arrays {
			t.Errorf("%s: traversal sites=%d, want %d", tc.source, got, tc.arrays)
		}
	}
}
