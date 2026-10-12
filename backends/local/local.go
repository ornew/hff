// Package local builds pure, concurrent-safe in-process HFF filters.
package local

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

// Config controls per-operation validation and evaluation limits.
// Zero limit fields select finite defaults. UnlimitedLimits is explicit.
type Config struct {
	Limits          hff.Limits
	UnlimitedLimits bool
	TimeZones       *TimeZoneProfile
	Methods         []MethodImplementation
}
type Option struct{ apply func(*Config) error }

func WithLimits(l hff.Limits) Option {
	return Option{func(c *Config) error { c.Limits = l; c.UnlimitedLimits = false; return nil }}
}
func WithUnlimitedLimits() Option {
	return Option{func(c *Config) error { c.UnlimitedLimits = true; return nil }}
}
func WithConfig(config *Config) Option {
	return Option{func(c *Config) error {
		if config == nil {
			return configuration("nil Config")
		}
		*c = *config
		return nil
	}}
}
func configuration(message string) error {
	return &hff.Error{Code: hff.ConfigurationError, Message: message}
}

// Filter snapshots its configuration. Each call owns independent evaluation state.
type Filter struct {
	program   plan.Program
	config    Config
	timeZones zoneSnapshot
	methods   map[string]MethodImplementation
}

// Build validates local support without evaluating a record.
// Options apply left to right; later options replace earlier settings.
func Build(ctx context.Context, program hff.Program, options ...Option) (*Filter, error) {
	if ctx == nil {
		return nil, configuration("nil context")
	}
	if err := cancelled(ctx); err != nil {
		return nil, err
	}
	if !program.Valid() {
		return nil, configuration("invalid Program")
	}
	c := Config{Limits: program.Limits(), UnlimitedLimits: program.UnlimitedLimits()}
	for _, o := range options {
		if o.apply == nil {
			return nil, configuration("invalid Option")
		}
		if err := o.apply(&c); err != nil {
			return nil, err
		}
	}
	limits, err := c.Limits.Resolve()
	if err != nil {
		return nil, configuration(err.Error())
	}
	c.Limits = limits
	if c.UnlimitedLimits {
		c.Limits = hff.Limits{}
	}
	p := plan.Program(program)
	zones, err := snapshotZones(ctx, c.TimeZones)
	if err != nil {
		return nil, err
	}
	// Evaluation keeps only privately parsed locations, not caller-owned data.
	c.TimeZones = nil
	methods, err := snapshotMethods(c.Methods)
	if err != nil {
		return nil, err
	}
	c.Methods = nil
	if err := checkSupported(ctx, p.Root(), methods); err != nil {
		return nil, err
	}
	return &Filter{program: p, config: c, timeZones: zones, methods: methods}, nil
}
func checkSupported(ctx context.Context, n plan.Node, methods map[string]MethodImplementation) error {
	if err := cancelled(ctx); err != nil {
		return err
	}
	if n.Kind() == plan.CallNode {
		method, _ := n.Method()
		for _, signature := range method.Signatures() {
			if declaration, extension := signature.Declaration(); extension {
				implementation, ok := methods[signature.ID()]
				if !ok || !core.MethodDeclaration(implementation.declaration).Equal(declaration) {
					span := n.Span()
					return &hff.Error{Code: hff.BackendError, Message: "missing or incompatible implementation for " + signature.ID(), Span: &span}
				}
				continue
			}
			if !supportedMethod(signature.ID()) {
				return &hff.Error{Code: hff.BackendError, Message: fmt.Sprintf("local backend does not support %s", signature.ID())}
			}
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if err := checkSupported(ctx, child, methods); err != nil {
			return err
		}
	}
	return nil
}
func cancelled(ctx context.Context) error {
	return core.CheckContext(ctx)
}

// Match validates the complete record before evaluating the filter.
// A completed null/no-value predicate is false; cancellation and limits are errors.
func (f *Filter) Match(ctx context.Context, record hff.Value) (bool, error) {
	if f == nil || !f.program.Valid() {
		return false, configuration("invalid Filter")
	}
	input, err := core.ValidateInput(ctx, f.program.Schema(), core.Value(record), core.ValidationConfig{Limits: f.config.Limits, Unlimited: f.config.UnlimitedLimits})
	if err != nil {
		return false, err
	}
	return f.match(ctx, input.Value())
}

// MatchInput reuses validation only for an exactly matching schema contract.
func (f *Filter) MatchInput(ctx context.Context, input hff.Input) (bool, error) {
	if f == nil || !f.program.Valid() {
		return false, configuration("invalid Filter")
	}
	if ctx == nil {
		return false, configuration("nil context")
	}
	if err := cancelled(ctx); err != nil {
		return false, err
	}
	if !input.Compatible(hff.Type(f.program.Schema())) {
		return false, &hff.Error{Code: hff.InputError, Message: "validated input has a different or invalid schema contract"}
	}
	return f.match(ctx, core.Value(input.Value()))
}
func (f *Filter) match(ctx context.Context, record core.Value) (bool, error) {
	state := evaluation{ctx: ctx, record: record, limits: f.config.Limits, timeZones: f.timeZones, methods: f.methods}
	value, err := state.eval(f.program.Root())
	if err != nil {
		return false, err
	}
	if err := cancelled(ctx); err != nil {
		return false, err
	}
	return truth(value), nil
}
