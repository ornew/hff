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

func TestParsedDocumentsAreSharedOnlyWithinScope(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.String("data")), schema.String("other"), schema.List("items", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source      string
		validations int
	}{
		{`data:fromjson:exists | data:fromjson=null`, 1},
		{`data:fromjson!:exists | data:fromjson=null`, 1},
		{`data:fromjson:exists other:fromjson:exists`, 2},
		{`data:fromjson:exists items:any(data=>data:fromjson:exists)`, 2},
		{`items:any(x=>x:fromjson:type=x:fromjson:type)`, 1},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("data")), hffsql.Bind(hff.Path{"other"}, hffsql.Column("other")), hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("items"), hffsql.JSON)))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(q.Where(), "IS JSON WITH UNIQUE KEYS"); got != tc.validations {
			t.Fatalf("%s: validations=%d want%d", tc.source, got, tc.validations)
		}
	}
}
