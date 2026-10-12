// Package govalue provides an optional schema-checked Go-to-HFF adapter.
// Reflection is confined to this package; HFF engines use explicit values.
package govalue

import (
	"context"
	"reflect"

	"github.com/ornew/hff"
	convert "github.com/ornew/hff/internal/govalue"
)

// StructMapping explicitly names direct exported Go fields for HFF object keys.
// Fields maps HFF names to Go names. Unmapped Go fields are omitted.
type StructMapping struct {
	Type   reflect.Type
	Fields map[string]string
}

type Config struct {
	Schema  hff.Type
	Structs []StructMapping
	// MaxInputDepth and MaxInputValues bound both Go traversal (including
	// pointer/interface wrappers) and subsequent HFF validation independently.
	Limits          hff.Limits
	UnlimitedLimits bool
}

// Converter owns a snapshot of its configuration and supports concurrent calls.
// Source records must not be mutated while Convert is running.
type Converter struct {
	schema    hff.Type
	fields    convert.Fields
	limits    hff.Limits
	unlimited bool
}

type Option struct{ apply func(*Config) error }

func WithConfig(config *Config) Option {
	return Option{func(out *Config) error {
		if config == nil {
			return configuration("nil converter configuration")
		}
		*out = *config
		return nil
	}}
}
func WithSchema(schema hff.Type) Option {
	return Option{func(out *Config) error { out.Schema = schema; return nil }}
}
func WithStructs(mappings ...StructMapping) Option {
	return Option{func(out *Config) error { out.Structs = mappings; return nil }}
}
func WithLimits(limits hff.Limits) Option {
	return Option{func(out *Config) error { out.Limits = limits; out.UnlimitedLimits = false; return nil }}
}
func WithUnlimitedLimits() Option {
	return Option{func(out *Config) error { out.UnlimitedLimits = true; return nil }}
}

func configuration(message string) error {
	return &hff.Error{Code: hff.ConfigurationError, Message: message}
}

// New applies options from left to right, validates the result, and copies it.
// An explicit valid schema is required. Zero limits select finite defaults.
func New(options ...Option) (*Converter, error) {
	var config Config
	for _, option := range options {
		if option.apply == nil {
			return nil, configuration("invalid converter option")
		}
		if err := option.apply(&config); err != nil {
			return nil, err
		}
	}
	if err := config.Schema.Check(); err != nil {
		return nil, configuration(err.Error())
	}
	limits, err := config.Limits.Resolve()
	if err != nil {
		return nil, configuration(err.Error())
	}
	if config.UnlimitedLimits {
		limits = hff.Limits{}
	}
	fields := make(convert.Fields, len(config.Structs))
	for _, mapping := range config.Structs {
		if mapping.Type == nil || mapping.Type.Kind() != reflect.Struct {
			return nil, configuration("struct mapping requires a Go struct type")
		}
		if _, exists := fields[mapping.Type]; exists {
			return nil, configuration("duplicate Go struct mapping")
		}
		copyFields := make(map[string]string, len(mapping.Fields))
		for name, goName := range mapping.Fields {
			field, ok := mapping.Type.FieldByName(goName)
			if !ok || field.PkgPath != "" || len(field.Index) != 1 {
				return nil, configuration("struct mapping requires direct exported Go fields")
			}
			copyFields[name] = goName
		}
		fields[mapping.Type] = copyFields
	}
	return &Converter{schema: config.Schema, fields: fields, limits: limits, unlimited: config.UnlimitedLimits}, nil
}

// Convert preserves Go numeric representations without coercion or tag inference.
// Nil pointers, maps, slices, interfaces, and nil itself become explicit null.
// Non-nil empty containers remain empty. Cycles, unsupported representations,
// invalid text, and schema mismatches produce typed errors, never partial values.
func (c *Converter) Convert(ctx context.Context, source any) (hff.Value, error) {
	if c == nil || c.schema.Kind() == hff.InvalidType {
		return hff.Value{}, configuration("invalid converter")
	}
	v, err := convert.Convert(ctx, source, c.fields, c.limits)
	if err != nil {
		return hff.Value{}, err
	}
	input, err := hff.ValidateInput(ctx, c.schema, v, hff.ValidationConfig{Limits: c.limits, Unlimited: c.unlimited})
	if err != nil {
		return hff.Value{}, err
	}
	return input.Value(), nil
}
