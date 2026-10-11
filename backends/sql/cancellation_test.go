package sql_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
)

// Cancel at a deterministic checkpoint rather than relying on goroutine timing.
type buildCancellationContext struct {
	context.Context
	checks atomic.Int64
	after  int64
	cancel context.CancelFunc
}

func (c *buildCancellationContext) Err() error {
	if count := c.checks.Add(1); c.after > 0 && count >= c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPostgreSQLBuildCancellationDuringStructuredLowering(t *testing.T) {
	e := compositeEngine(t)
	program, err := e.Compile(context.Background(), `profile.people:map(x=>x.name):contains("Alice") profile.age>0`)
	if err != nil {
		t.Fatal(err)
	}
	for _, representation := range []struct {
		name    string
		binding hffsql.Binding
	}{
		{"Composite", hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))},
		{"JSON", hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), hffsql.JSON)},
		{"JSONB", hffsql.BindJSON(hff.Path{"profile"}, hffsql.Column("records", "profile"), hffsql.JSONB)},
	} {
		t.Run(representation.name, func(t *testing.T) {
			options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(representation.binding)}
			baseline := &buildCancellationContext{Context: context.Background()}
			if _, err := hffsql.Build(baseline, program, options...); err != nil {
				t.Fatal(err)
			}
			checks := baseline.checks.Load()
			if checks < 3 {
				t.Fatal("structured build did not check cancellation during work")
			}
			for _, after := range []int64{1, checks / 2, checks - 1} {
				base, cancel := context.WithCancel(context.Background())
				ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
				query, err := hffsql.Build(ctx, program, options...)
				cancel()
				requireCode(t, err, hff.CancelledError)
				if query != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled build returned a partial query or lost its cause: query=%v err=%v", query, err)
				}
			}
		})
	}
}
