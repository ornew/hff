// Package engine configures common HFF compilation and validates host records.
// An Engine snapshots a schema, capability policy, method declarations, and
// limits. Compile produces a reusable Program; a backend builds that Program
// for evaluation. Validate creates an immutable Input reusable by a backend's
// MatchInput method when the schema contracts match.
package engine

import (
	"context"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/compiler"
	"github.com/ornew/hff/internal/core"
)

// Config is the complete configuration accepted by WithConfig. New validates
// and snapshots the schema, declarations, capabilities, and limits. A valid
// struct or map root schema and an explicitly selected capability policy are
// required.
type Config struct {
	// Schema is the required root struct or map schema.
	Schema hff.Type
	// Capabilities is the explicit permission set used by Compile.
	Capabilities capabilities.Set
	// Limits selects compiler and input-validation ceilings; zero fields default.
	Limits hff.Limits
	// UnlimitedLimits explicitly disables configured limits.
	UnlimitedLimits bool
	// Methods declares extension signatures; implementations belong to backends.
	Methods []hff.MethodDeclaration
}

// Option changes one Engine setting. New applies options from left to right.
type Option struct{ apply func(*Config) error }

// WithSchema sets the root input schema. New requires a valid struct or map.
func WithSchema(t hff.Type) Option { return Option{func(c *Config) error { c.Schema = t; return nil }} }

// WithCapabilities sets the explicit capability policy used during compilation.
func WithCapabilities(s capabilities.Set) Option {
	return Option{func(c *Config) error { c.Capabilities = s; return nil }}
}

// WithMethods replaces the extension declarations in the configuration.
func WithMethods(methods ...hff.MethodDeclaration) Option {
	owned := append([]hff.MethodDeclaration(nil), methods...)
	return Option{func(c *Config) error { c.Methods = append([]hff.MethodDeclaration(nil), owned...); return nil }}
}

// WithLimits replaces limits and disables UnlimitedLimits.
func WithLimits(l hff.Limits) Option {
	return Option{func(c *Config) error { c.Limits = l; c.UnlimitedLimits = false; return nil }}
}

// WithUnlimitedLimits explicitly disables limits resolved by this Engine.
func WithUnlimitedLimits() Option {
	return Option{func(c *Config) error { c.UnlimitedLimits = true; return nil }}
}

// WithConfig copies a complete configuration. Later options override fields
// set by this option; a nil config is a ConfigurationError.
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

// New validates options, schema, capabilities, extension declarations, and
// limits. The returned Engine snapshots its configuration and supports
// concurrent Compile and Validate calls.
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

// Schema returns the immutable root input schema.
func (e *Engine) Schema() hff.Type { return e.config.Schema }

// Capabilities returns the selected compilation policy.
func (e *Engine) Capabilities() capabilities.Set { return e.config.Capabilities }

// Limits returns resolved common compiler and input-validation limits.
func (e *Engine) Limits() hff.Limits { return e.config.Limits }

// Compile checks syntax, types, and capabilities and produces a reusable plan.
// It returns typed syntax, type, capability, limit, or cancellation errors. It
// does not construct a local evaluator or a backend query.
func (e *Engine) Compile(ctx context.Context, source string) (hff.Program, error) {
	p, err := compiler.Compile(ctx, source, compiler.Config{
		Schema: core.Type(e.config.Schema), Capabilities: e.config.Capabilities,
		Limits: e.config.Limits, Unlimited: e.config.UnlimitedLimits, Methods: e.methods(),
	})
	return hff.Program(p), err
}

// Validate validates a complete host record against this Engine's schema and
// input limits. It returns an immutable Input suitable for repeated matching
// by a backend whose Program uses the same schema contract. Invalid records,
// cancellation, and exceeded limits return typed errors with no partial Input.
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
