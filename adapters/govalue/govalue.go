package govalue

import (
	"context"
	"reflect"

	"github.com/ornew/hff"
	convert "github.com/ornew/hff/internal/govalue"
)

// StructMapping explicitly maps an HFF object type to one Go struct type.
// Fields maps HFF object keys to direct exported Go field names; embedded or
// promoted fields are not accepted by this explicit mapping. Unmapped Go
// fields are omitted from the converted object.
type StructMapping struct {
	// Type is the exact Go struct type to map, typically reflect.TypeFor[T]().
	Type reflect.Type
	// Fields maps HFF field names to Go field names.
	Fields map[string]string
}

// Config describes an explicit Go-to-HFF conversion. Schema is required;
// Structs maps Go structs to HFF object fields. Only input depth/value limits
// are used, both while traversing Go wrappers and while validating the HFF
// result. Other Limits fields do not impose conversion budgets.
type Config struct {
	// Schema is the required explicit HFF schema for New.
	Schema hff.Type
	// Structs maps concrete Go struct types to HFF object keys.
	Structs []StructMapping
	// MaxInputDepth and MaxInputValues bound both Go traversal (including
	// pointer/interface wrappers) and subsequent HFF validation independently.
	Limits hff.Limits
	// UnlimitedLimits explicitly disables the adapter's input depth/value limits.
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

// Option changes one Converter setting. New applies options from left to right.
type Option struct{ apply func(*Config) error }

// WithConfig copies a complete Converter configuration. Nil is rejected, and
// later options replace fields copied from this value.
func WithConfig(config *Config) Option {
	return Option{func(out *Config) error {
		if config == nil {
			return configuration("nil converter configuration")
		}
		*out = *config
		return nil
	}}
}

// WithSchema sets the required explicit HFF root schema for New.
func WithSchema(schema hff.Type) Option {
	return Option{func(out *Config) error { out.Schema = schema; return nil }}
}

// WithStructs sets explicit Go struct mappings. New copies the slice and maps.
func WithStructs(mappings ...StructMapping) Option {
	return Option{func(out *Config) error { out.Structs = mappings; return nil }}
}

// WithLimits selects input traversal/validation limits. Other limit fields are
// validated but do not control conversion; zero input limits resolve to finite defaults.
func WithLimits(limits hff.Limits) Option {
	return Option{func(out *Config) error { out.Limits = limits; out.UnlimitedLimits = false; return nil }}
}

// WithUnlimitedLimits disables this adapter's input depth/value limits.
func WithUnlimitedLimits() Option {
	return Option{func(out *Config) error { out.UnlimitedLimits = true; return nil }}
}

func configuration(message string) error {
	return &hff.Error{Code: hff.ConfigurationError, Message: message}
}

// New applies options from left to right, validates and snapshots the result,
// and requires an explicit valid schema. Zero limits select finite input
// defaults. The returned Converter supports concurrent calls as long as each
// source value remains unchanged during conversion.
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

// Convert maps one Go value to an HFF Value and validates it against the
// Converter's explicit schema. It preserves numeric representations without
// coercion and does not infer names from tags. Nil pointers, maps, slices,
// interfaces, and nil itself become explicit null; non-nil empty containers
// remain empty. Cycles, unsupported representations, invalid text, cancellation,
// exceeded input limits, and schema mismatches return typed errors without a
// partial Value. Do not mutate source while conversion is running.
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
