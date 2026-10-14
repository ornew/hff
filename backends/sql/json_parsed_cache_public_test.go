package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestParsedJSONFieldsShareOnlyStableParents(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.String("data")), schema.String("other"), schema.List("items", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		keys   int
	}{
		{`data:fromjson.a=1 data:fromjson.a=2`, 1},
		{`data:fromjson.a.b=1 data:fromjson.a.b=2`, 2},
		{`data:fromjson.a:type=data:fromjson.a:type`, 1},
		{`data:fromjson.a=1 other:fromjson.a=2`, 2},
		{`data!:fromjson.a=1 data:fromjson.a=2`, 2},
		{`data:fromjson!.a=1 data:fromjson.a=2`, 2},
		{`items:any(data=>data:fromjson.a=1 data:fromjson.a=2)`, 1},
		{`items:any(data=>data:fromjson.a=1) items:any(data=>data:fromjson.a=2)`, 2},
		{`items:any(x=>x:fromjson.a=1 items:any(x=>x:fromjson.a=2))`, 2},
		{`items:any(x=>data:fromjson.a=1 items:any(x=>data:fromjson.a=2))`, 2},
	} {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data")),
			hffsql.Bind(hff.Path{"other"}, hffsql.Column("records", "other")),
			hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON),
		))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		keys := 0
		for _, arg := range q.Args() {
			if key, ok := arg.(string); ok && (key == "a" || key == "b") {
				keys++
			}
		}
		if keys != tc.keys {
			t.Errorf("%s: extracted field keys=%d, want %d", tc.source, keys, tc.keys)
		}
	}
}
