package sql_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func predicateProgram(t *testing.T) hff.Program {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(schema.String("name"))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `profile.name="Alice"`)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestStructuredPredicateQuotesColumnsAndBindsValues(t *testing.T) {
	p := predicateProgram(t)
	injected := `');DROP TABLE accounts;--`
	present := hffsql.And(hffsql.Equal(hffsql.ColumnValue(hffsql.Column("records", "tenant"), schema.StringType), hffsql.Literal(hff.String(injected))), hffsql.Not(hffsql.IsNull(hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType))))
	binding := hffsql.BindStruct(hff.Path{"profile"}, present, hffsql.Boolean(false), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")))
	query, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Where(), injected) {
		t.Fatal("literal interpolated")
	}
	if len(query.Args()) != 2 || query.Args()[0] != injected || query.Args()[1] != "Alice" {
		t.Fatalf("args %v", query.Args())
	}
	if !strings.Contains(query.Where(), `"records"."tenant"`) {
		t.Fatal("column not quoted")
	}
	var wg sync.WaitGroup
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil || q.Where() != query.Where() {
				t.Errorf("reuse %v", err)
			}
		}()
	}
	wg.Wait()
}
func TestStructuredPredicateRejectsInvalidTypes(t *testing.T) {
	p := predicateProgram(t)
	for _, predicate := range []hffsql.SQLPredicate{
		hffsql.Equal(hffsql.ColumnValue(hffsql.Column("id"), schema.IntType), hffsql.Literal(hff.String("1"))),
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("id"), schema.IntType)),
		hffsql.IsNull(hffsql.Scalar{}), hffsql.And(), hffsql.Or(),
		hffsql.IsNull(hffsql.ColumnValue(hffsql.Column("data"), schema.BytesType)),
	} {
		binding := hffsql.BindStruct(hff.Path{"profile"}, predicate, hffsql.Boolean(false), hffsql.Bind(hff.Path{"name"}, hffsql.Column("name")))
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err == nil {
			t.Fatal("invalid predicate accepted")
		}
	}
}

func TestStructuredPredicateAcceptsNativeDuration(t *testing.T) {
	p := predicateProgram(t)
	present := hffsql.Equal(
		hffsql.ColumnValue(hffsql.Column("records", "elapsed_ns"), schema.DurationType),
		hffsql.Literal(hff.Duration(time.Nanosecond)),
	)
	binding := hffsql.BindStruct(hff.Path{"profile"}, present, hffsql.Boolean(false),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")))
	query, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where(), `"records"."elapsed_ns"`) || !strings.Contains(query.Where(), "::bigint") {
		t.Fatal("duration predicate did not use a bigint nanosecond comparison")
	}
	if len(query.Args()) != 2 || query.Args()[0] != int64(1) || query.Args()[1] != "Alice" {
		t.Fatalf("args %v", query.Args())
	}
}
