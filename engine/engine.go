// Package engine configures common HFF compilation and validates host records.
package engine

import (
	"context"
	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
)

type Config struct {
	Schema          hff.Type
	Capabilities    capabilities.Set
	Limits          hff.Limits
	UnlimitedLimits bool
}

// Option is opaque and applied left to right by New.
type Option struct{ apply func(*Config) error }

func WithSchema(t hff.Type) Option { return Option{func(c *Config) error { c.Schema = t; return nil }} }
func WithCapabilities(s capabilities.Set) Option {
	return Option{func(c *Config) error { c.Capabilities = s; return nil }}
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
	if err := c.Capabilities.Check(); err != nil {
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
func (e *Engine) Validate(ctx context.Context, record hff.Value) (hff.Input, error) {
	return hff.ValidateInput(ctx, e.config.Schema, record, hff.ValidationConfig{Limits: e.config.Limits, Unlimited: e.config.UnlimitedLimits})
}
