package sql_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
)

// Mutation of source exercises validated IR combinations, including rejected
// backend operations. This is a builder invariant test, not a SQL semantics test.
func FuzzPostgreSQLBuild(f *testing.F) {
	e := compositeEngine(f)
	for _, source := range compositeQueries() {
		f.Add(source)
	}
	for _, source := range []string{
		`profile.people:any(x=>x.name:matches("A"))`,
		`profile.people:map(x=>x:keys):flatmap(x=>x):unique`,
		`profile.people:foldl({},(a,x)=>x)=null`,
		`profile:tojson:fromjson:[object]:exists`,
		`profile.scores:foldr(0,(x,a)=>a+x)>0`,
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 4096 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		check := func(err error) bool {
			if err == nil {
				return false
			}
			var detail *hff.Error
			if !errors.As(err, &detail) {
				t.Fatalf("untyped failure: %v", err)
			}
			if detail.Span != nil && (detail.Span.Start < 0 || detail.Span.End > len(source) || detail.Span.End < detail.Span.Start) {
				t.Fatalf("invalid diagnostic span: %+v", detail.Span)
			}
			return true
		}
		program, err := e.Compile(ctx, source)
		if check(err) {
			return
		}
		for _, binding := range []hffsql.Binding{
			hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile")),
			hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), hffsql.JSON),
			hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), hffsql.JSONB),
		} {
			first, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if check(err) {
				continue
			}
			second, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if check(err) {
				if ctx.Err() != nil {
					return
				}
				t.Fatal("repeated build failed after a successful build")
			}
			if first.Where() == "" || first.Where() != second.Where() || !reflect.DeepEqual(first.Args(), second.Args()) {
				t.Fatal("repeated builds produced inconsistent predicates or parameters")
			}
		}
	})
}
