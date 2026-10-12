// Package engine configures common HFF compilation and validates host records.
package engine

import (
	"context"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/compiler"
	"github.com/ornew/hff/internal/core"
)

type Config struct {
	Schema          hff.Type
	Capabilities    capabilities.Set
	Limits          hff.Limits
	UnlimitedLimits bool
	Methods         []hff.MethodDeclaration
}

// Option is opaque and applied left to right by New.
type Option struct{ apply func(*Config) error }

func WithSchema(t hff.Type) Option { return Option{func(c *Config) error { c.Schema = t; return nil }} }
func WithCapabilities(s capabilities.Set) Option {
	return Option{func(c *Config) error { c.Capabilities = s; return nil }}
}
func WithMethods(methods ...hff.MethodDeclaration) Option {
	owned := append([]hff.MethodDeclaration(nil), methods...)
	return Option{func(c *Config) error { c.Methods = append([]hff.MethodDeclaration(nil), owned...); return nil }}
}
func WithLimits(l hff.Limits) Option {
	return Option{func(c *Config) error { c.Limits = l; c.UnlimitedLimits = false; return nil }}
}
func WithUnlimitedLimits() Option {
	return Option{func(c *Config) error { c.UnlimitedLimits = true; return nil }}
}
func WithConfig(config *Config) Option {
	return Option{func(c *Config) error {
		if config == nil {
			return &hff.Error{Code: hff.ConfigurationError, Message: "nil Config"}
		}
		*c = *config
		return nil
	}}
}

// Engine is immutable and supports concurrent calls.
type Engine struct{ config Config }

func New(options ...Option) (*Engine, error) {
	var c Config
	for _, option := range options {
		if option.apply == nil {
			return nil, &hff.Error{Code: hff.ConfigurationError, Message: "invalid Option"}
		}
		if err := option.apply(&c); err != nil {
			return nil, err
		}
	}
	if err := c.Schema.Check(); err != nil {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: err.Error()}
	}
	if c.Schema.Kind() != hff.StructType && c.Schema.Kind() != hff.MapType {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: "root schema must be a struct or map"}
	}
	declarations := make([]core.MethodDeclaration, len(c.Methods))
	ids := make([]string, len(c.Methods))
	for i, method := range c.Methods {
		declarations[i] = core.MethodDeclaration(method)
		ids[i] = method.ID()
	}
	if err := compiler.CheckMethods(declarations); err != nil {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: err.Error()}
	}
	c.Methods = append([]hff.MethodDeclaration(nil), c.Methods...)
	if err := c.Capabilities.CheckWithMethods(ids...); err != nil {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: err.Error()}
	}
	limits, err := c.Limits.Resolve()
	if err != nil {
		return nil, &hff.Error{Code: hff.ConfigurationError, Message: err.Error()}
	}
	c.Limits = limits
	if c.UnlimitedLimits {
		c.Limits = hff.Limits{}
	}
	return &Engine{config: c}, nil
}
func (e *Engine) Schema() hff.Type               { return e.config.Schema }
func (e *Engine) Capabilities() capabilities.Set { return e.config.Capabilities }
func (e *Engine) Limits() hff.Limits             { return e.config.Limits }

// Compile checks syntax, types, and capabilities and produces a reusable plan.
// It does not construct a local evaluator or a backend query.
func (e *Engine) Compile(ctx context.Context, source string) (hff.Program, error) {
	p, err := compiler.Compile(ctx, source, compiler.Config{
		Schema: core.Type(e.config.Schema), Capabilities: e.config.Capabilities,
		Limits: e.config.Limits, Unlimited: e.config.UnlimitedLimits, Methods: e.methods(),
	})
	return hff.Program(p), err
}
func (e *Engine) Validate(ctx context.Context, record hff.Value) (hff.Input, error) {
	return hff.ValidateInput(ctx, e.config.Schema, record, hff.ValidationConfig{Limits: e.config.Limits, Unlimited: e.config.UnlimitedLimits})
}

func (e *Engine) methods() []core.MethodDeclaration {
	out := make([]core.MethodDeclaration, len(e.config.Methods))
	for i, m := range e.config.Methods {
		out[i] = core.MethodDeclaration(m)
	}
	return out
}

// Methods returns a copy of the configured immutable declarations.
func (e *Engine) Methods() []hff.MethodDeclaration {
	return append([]hff.MethodDeclaration(nil), e.config.Methods...)
}
