package sql_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func numericDispatchEngine(t testing.TB) (*engine.Engine, []hffsql.Binding) {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("choice", schema.Union(schema.StringType, schema.IntType, schema.NullType)),
		schema.Field("fchoice", schema.Union(schema.StringType, schema.FloatType, schema.NullType)),
		schema.Nullable(schema.Int("ceiling")), schema.Nullable(schema.Float("threshold")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e, []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"choice"}, hffsql.Column("records", "choice"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"fchoice"}, hffsql.Column("records", "fchoice"), hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "float_meta"))),
		hffsql.Bind(hff.Path{"ceiling"}, hffsql.Column("records", "ceiling")),
		hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("records", "threshold")),
	}
}

func TestNumericDispatchOmitsSchemaImpossibleFamilies(t *testing.T) {
	ctx := context.Background()
	e, bindings := numericDispatchEngine(t)
	for _, tc := range []struct {
		source     string
		mixed      bool
		floatInput bool
	}{
		{`choice>10`, false, false}, {`choice=10`, false, false},
		{`ceiling<choice`, false, false}, {`fchoice>1.5`, false, true},
		{`threshold<fchoice`, false, true},
		{`choice>1.5`, true, true}, {`threshold<choice`, true, true},
		{`fchoice>1`, true, true}, {`ceiling<fchoice`, true, true},
	} {
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if got := strings.Contains(query.Where(), "floor("); got != tc.mixed {
			t.Fatalf("%s: exact mixed comparison present=%v, want %v", tc.source, got, tc.mixed)
		}
		if !tc.floatInput && strings.Contains(query.Where(), "__hff_float_") {
			t.Fatalf("%s retained an impossible Float parser", tc.source)
		}
		if tc.source == "choice>10" && !reflect.DeepEqual(query.Args(), []any{int64(10)}) {
			t.Fatal("numeric dispatch changed arguments:", query.Args())
		}
	}
}
