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

type metadataPlacement struct {
	name     string
	schema   hff.Type
	bindings []hffsql.Binding
	query    func(string) string
	record   func(hff.Value) hff.Value
}

func metadataPlacements(tb testing.TB) []metadataPlacement {
	tb.Helper()
	doc := schema.Nullable(schema.Map("doc", schema.AnyType))
	item := schema.Struct(doc)
	jsonBinding := func(alias string) hffsql.Binding {
		return hffsql.BindJSON(hff.Path{"doc"}, hffsql.Column(alias, "doc"), hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column(alias, "metadata")))
	}
	wrap := func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"doc": v}) }
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("children"), "child"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("child", "parent_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType))),
		hffsql.WithRelationItem(item, hffsql.BindStruct(nil, hffsql.Boolean(true), hffsql.Boolean(false), jsonBinding("child"))),
	)
	if err != nil {
		tb.Fatal(err)
	}
	return []metadataPlacement{
		{"Direct", item, []hffsql.Binding{jsonBinding("records")}, func(q string) string { return "doc" + q }, wrap},
		{"Flattened", schema.Struct(schema.Field("profile", item)),
			[]hffsql.Binding{hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false), jsonBinding("records"))},
			func(q string) string { return "profile.doc" + q },
			func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"profile": wrap(v)}) }},
		{"Relation", schema.Struct(schema.List("items", item)), []hffsql.Binding{hffsql.BindRelation(hff.Path{"items"}, relation)},
			func(q string) string { return "items:any(x=>x.doc" + q + ")" },
			func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"items": hff.List(wrap(v))}) }},
	}
}

var metadataPlacementQueries = []struct{ name, suffix string }{
	{"DocumentRoot", `:type="object"`},
	{"NestedScalar", `.nested.price>10.5`},
}

func metadataPlacementEngine(tb testing.TB, placement metadataPlacement) *engine.Engine {
	tb.Helper()
	e, err := engine.New(engine.WithSchema(placement.schema), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func BenchmarkPostgreSQLMetadataPlacementBuild(b *testing.B) {
	ctx := context.Background()
	for _, placement := range metadataPlacements(b) {
		e := metadataPlacementEngine(b, placement)
		for _, query := range metadataPlacementQueries {
			b.Run(placement.name+"/"+query.name, func(b *testing.B) {
				p, err := e.Compile(ctx, placement.query(query.suffix))
				if err != nil {
					b.Fatal(err)
				}
				options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(placement.bindings...)}
				q, err := hffsql.Build(ctx, p, options...)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := hffsql.Build(ctx, p, options...); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(len(q.Where())), "sql-bytes")
				b.ReportMetric(float64(len(q.Args())), "parameters")
			})
		}
	}
}
