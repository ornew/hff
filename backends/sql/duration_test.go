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

func TestComputedDurationRepresentationBoundaries(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:duration:in(data:fromjson)`, `[data:duration]:size=1`, `{"d":data:duration}:size=1`, `[data:duration]:json="[]"`, `data:fromjson:[list]:map(x=>x:duration):size=1`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("data"))))
		detail := requireCode(t, err, hff.BackendError)
		if detail.Span == nil {
			t.Fatalf("%s: representation rejection lacks source span", source)
		}
	}
	for _, source := range []string{`data:duration:in(data:fromjson:[string|int]):exists`, `[data:duration:nanoseconds]:size=1`, `[data:duration:string]:size=1`, `data:duration:[duration]:exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("data")))); err != nil {
			t.Fatal(source, err)
		}
	}
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"string.duration", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds"} {
		feature, ok := profile.Feature(capabilities.Method(id))
		if !ok || feature.Level != hff.Conditional {
			t.Fatalf("computed duration profile missing %s", id)
		}
	}
}

func TestStoredDurationScalarsRemainRejected(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{schema.DurationType, schema.Union(schema.DurationType, schema.NullType), schema.Union(schema.DurationType, schema.StringType, schema.NullType)} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `data:type="duration"`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{"", hffsql.JSON, hffsql.JSONB} {
			binding := hffsql.Bind(hff.Path{"data"}, hffsql.Column("data"))
			if format != "" {
				binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), format)
			}
			_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err == nil {
				t.Errorf("%s %s: decoded an unsupported stored Duration", typ.Key(), format)
				continue
			}
			requireCode(t, err, hff.BackendError)
		}
	}
}
