package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/schema"
)

func TestCancellationCausesAcrossOperations(t *testing.T) {
	e := newEngine(t, schema.Struct(schema.Int("age")))
	program, err := e.Compile(context.Background(), `age>=18`)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(context.Background(), program)
	if err != nil {
		t.Fatal(err)
	}
	record := hff.Object(map[string]hff.Value{"age": hff.Int(20)})
	input, err := e.Validate(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	for _, deadline := range []bool{false, true} {
		name := "Canceled"
		status := context.Canceled
		cause := errors.New("host requested cancellation")
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			name, status = "Deadline", context.DeadlineExceeded
			ctx, cancel = context.WithDeadlineCause(context.Background(), time.Now().Add(-time.Second), cause)
		} else {
			var cancelCause context.CancelCauseFunc
			ctx, cancelCause = context.WithCancelCause(context.Background())
			cancelCause(cause)
			cancel = func() { cancelCause(nil) }
		}
		t.Run(name, func(t *testing.T) {
			defer cancel()
			for _, operation := range []struct {
				name string
				run  func(*testing.T) error
			}{
				{"Compile", func(t *testing.T) error {
					p, err := e.Compile(ctx, `age>=18`)
					if p.Valid() {
						t.Fatal("canceled compile returned a program")
					}
					return err
				}},
				{"Validate", func(t *testing.T) error {
					i, err := e.Validate(ctx, record)
					if i.Compatible(input.Schema()) {
						t.Fatal("canceled validation returned an input")
					}
					return err
				}},
				{"LocalBuild", func(t *testing.T) error {
					f, err := local.Build(ctx, program)
					if f != nil {
						t.Fatal("canceled build returned a filter")
					}
					return err
				}},
				{"SQLBuild", func(t *testing.T) error {
					q, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("age"))))
					if q != nil {
						t.Fatal("canceled build returned a query")
					}
					return err
				}},
				{"Match", func(t *testing.T) error {
					matched, err := filter.Match(ctx, record)
					if matched {
						t.Fatal("canceled evaluation returned a match")
					}
					return err
				}},
				{"MatchInput", func(t *testing.T) error {
					matched, err := filter.MatchInput(ctx, input)
					if matched {
						t.Fatal("canceled evaluation returned a match")
					}
					return err
				}},
			} {
				t.Run(operation.name, func(t *testing.T) {
					err := operation.run(t)
					requireCode(t, err, hff.CancelledError)
					if !errors.Is(err, status) || !errors.Is(err, cause) {
						t.Fatalf("lost cancellation status or host cause: %v", err)
					}
				})
			}
		})
	}
}
