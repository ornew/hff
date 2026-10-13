package sql_test

import (
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/schema"
)

func TestSchemaPairUsesBoundedContainerEquality(t *testing.T) {
	for _, typ := range []hff.Type{
		schema.Union(schema.StringType, schema.ListOf(schema.IntType)),
		schema.Union(schema.StringType, schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType)),
		schema.Union(schema.StringType, schema.Struct(schema.Int("i"), schema.Optional(schema.Float("f"))), schema.Struct(schema.Int("i"), schema.Optional(schema.String("label")))),
	} {
		q := pairDispatchQuery(t, typ, typ, `left=right`, hffsql.JSON, false)
		if strings.Contains(q.Where(), "WITH RECURSIVE") || strings.Contains(q.Where(), "json_agg(") || strings.Contains(q.Where(), "json_object_agg(") {
			t.Fatalf("known container schema retained generic tree normalization: %s", q.Where())
		}
		if !strings.Contains(q.Where(), "FULL JOIN") {
			t.Fatal("ordered/key-set equality was removed")
		}
	}
}

func TestSchemaPairKeepsAnyLeafFallback(t *testing.T) {
	typ := schema.Union(schema.StringType, schema.Struct(schema.Any("payload"), schema.Int("id")))
	q := pairDispatchQuery(t, typ, typ, `left=right`, hffsql.JSON, false)
	if strings.Count(q.Where(), "WITH RECURSIVE") != 1 {
		t.Fatal("unconstrained payload lost generic equality")
	}
	if len(q.Args()) != 2 {
		t.Fatalf("expected one parameter per known key: %v", q.Args())
	}
}

func TestSchemaPairDoesNotMergeUnrelatedNumericFields(t *testing.T) {
	typ := schema.Union(schema.StringType,
		schema.Struct(schema.Int("i"), schema.Optional(schema.Float("f"))),
		schema.Struct(schema.Int("i"), schema.Optional(schema.String("label"))),
	)
	// Int and Float at different keys are recoverable without metadata. The
	// equality helper must not flatten those keys into one ambiguous value type.
	q := pairDispatchQuery(t, typ, typ, `left=right`, hffsql.JSONB, false)
	if strings.Contains(q.Where(), "floor(") {
		t.Fatal("unrelated Int and Float fields retained mixed numeric comparison")
	}
}
