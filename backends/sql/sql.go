// Package sql builds parameterized SQL predicates without opening a database.
package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

// Dialect identifies an SQL translation contract. Build requires an explicit dialect.
type Dialect string

const PostgreSQL Dialect = "postgresql"

// ColumnRef is a sequence of literal SQL identifiers, never an SQL expression.
type ColumnRef struct{ parts []string }

func Column(parts ...string) ColumnRef { return ColumnRef{append([]string(nil), parts...)} }

// Binding maps a literal schema path to a database column.
type Binding struct {
	path      hff.Path
	column    ColumnRef
	format    JSONFormat
	composite bool
}

func Bind(path hff.Path, column ColumnRef) Binding {
	return Binding{path: append(hff.Path(nil), path...), column: Column(column.parts...)}
}

type Config struct {
	Dialect         Dialect
	Bindings        []Binding
	Methods         []MethodImplementation
	ExecutionLimits ExecutionLimits
}

type Option struct{ apply func(*Config) error }

func WithDialect(dialect Dialect) Option {
	return Option{func(c *Config) error { c.Dialect = dialect; return nil }}
}

func WithBindings(bindings ...Binding) Option {
	return Option{func(c *Config) error { c.Bindings = append([]Binding(nil), bindings...); return nil }}
}

func WithConfig(config *Config) Option {
	return Option{func(c *Config) error {
		if config == nil {
			return configuration("nil Config")
		}
		*c = *config
		c.Bindings = append([]Binding(nil), config.Bindings...)
		c.Methods = append([]MethodImplementation(nil), config.Methods...)
		return nil
	}}
}

// Query owns a reusable WHERE expression and driver parameters. It performs no I/O.
type Query struct {
	where   string
	args    []any
	profile hff.Profile
}

func (q *Query) Where() string {
	if q == nil {
		return ""
	}
	return q.where
}

// Args returns a fresh slice. Current parameter encodings are immutable scalars.
func (q *Query) Args() []any {
	if q == nil {
		return nil
	}
	return append([]any(nil), q.args...)
}

// Build validates support and translates the common typed plan. Options apply
// left to right, with later settings replacing earlier settings.
func Build(ctx context.Context, program hff.Program, options ...Option) (*Query, error) {
	if ctx == nil {
		return nil, configuration("nil context")
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if !program.Valid() {
		return nil, configuration("invalid Program")
	}
	var config Config
	for _, option := range options {
		if option.apply == nil {
			return nil, configuration("invalid Option")
		}
		if err := option.apply(&config); err != nil {
			return nil, err
		}
	}
	if config.Dialect != PostgreSQL {
		return nil, configuration("explicit supported SQL dialect required")
	}
	if err := checkExecutionLimits(config.ExecutionLimits); err != nil {
		return nil, err
	}
	methods, err := snapshotMethods(config.Methods, config.Dialect)
	if err != nil {
		return nil, err
	}
	b := builder{methods: methods, ctx: ctx, program: program, bindings: make(map[string]string), reserved: make(map[string]bool), jsonBindings: make(map[string]JSONFormat), jsonValues: make(map[string]JSONFormat), compositeBindings: make(map[string]bool)}
	if err := b.checkMethods(program.Root()); err != nil {
		return nil, err
	}
	columnKinds := make(map[string]hff.TypeKind)
	columnFormats := make(map[string]JSONFormat)
	columnJSONTypes := make(map[string]string)
	for _, binding := range config.Bindings {
		if len(binding.path) == 0 {
			return nil, configuration("empty schema binding path")
		}
		typ, err := boundType(program.Schema(), binding.path)
		if err != nil {
			return nil, configuration(err.Error())
		}
		key := pathKey(binding.path)
		if _, exists := b.bindings[key]; exists {
			return nil, configuration("duplicate schema binding path")
		}
		column, err := quoteColumn(binding.column)
		if err != nil {
			return nil, err
		}
		if binding.composite {
			kind, ok := jsonKind(typ)
			if !ok || kind != hff.StructType {
				return nil, configuration("composite binding requires an unambiguous struct schema")
			}
			if previous, exists := columnFormats[column]; exists && previous != "composite" {
				return nil, configuration("SQL column has conflicting physical formats")
			}
			columnFormats[column] = "composite"
			if previous, exists := columnJSONTypes[column]; exists && previous != typ.Key() {
				return nil, configuration("composite column has conflicting schema types")
			}
			columnJSONTypes[column] = typ.Key()
			b.compositeBindings[key] = true
			b.jsonBindings[key] = JSON
		} else if binding.format != "" {
			if binding.format != JSON && binding.format != JSONB {
				return nil, configuration("invalid JSON binding format")
			}
			if previous, exists := columnFormats[column]; exists && previous != binding.format {
				return nil, configuration("SQL column has conflicting physical formats")
			}
			columnFormats[column] = binding.format
			if previous, exists := columnJSONTypes[column]; exists && previous != typ.Key() {
				return nil, configuration("JSON column has conflicting schema types")
			}
			columnJSONTypes[column] = typ.Key()
			b.jsonBindings[key] = binding.format
		} else if kind, ok := scalarKind(typ); ok {
			if previous, exists := columnFormats[column]; exists && previous != "" {
				return nil, configuration("SQL column has conflicting physical formats")
			}
			columnFormats[column] = ""
			if previous, exists := columnKinds[column]; exists && previous != kind {
				return nil, configuration("SQL column has conflicting schema types")
			}
			columnKinds[column] = kind
		}
		b.bindings[key] = column
		for _, component := range binding.column.parts {
			b.reserved[component] = true
		}
	}
	for _, parent := range config.Bindings {
		if parent.format == "" && !parent.composite {
			continue
		}
		for _, child := range config.Bindings {
			if len(child.path) <= len(parent.path) {
				continue
			}
			if pathKey(child.path[:len(parent.path)]) == pathKey(parent.path) {
				return nil, configuration("structured binding overlaps a descendant binding")
			}
		}
	}
	root, err := b.expression(program.Root())
	if err != nil {
		return nil, err
	}
	where := truth(root)
	if len(b.steps) > 0 {
		where = "(SELECT " + where + " FROM " + strings.Join(b.steps, " CROSS JOIN LATERAL ") + ")"
	}
	profile, err := queryProfile(config.Dialect, program, methods)
	if err != nil {
		return nil, err
	}
	return &Query{where: where, args: append([]any(nil), b.args...), profile: profile}, nil
}

func configuration(message string) error {
	return &hff.Error{Code: hff.ConfigurationError, Message: message}
}

func checkContext(ctx context.Context) error {
	return core.CheckContext(ctx)
}

func pathKey(path []string) string {
	data, _ := json.Marshal(path)
	return string(data)
}

func quoteColumn(column ColumnRef) (string, error) {
	if len(column.parts) == 0 {
		return "", configuration("empty SQL column")
	}
	parts := make([]string, len(column.parts))
	for i, part := range column.parts {
		// PostgreSQL truncates identifiers beyond 63 bytes. Reject rather than
		// accidentally bind a different identifier after server truncation.
		if part == "" || len(part) > 63 || !utf8.ValidString(part) || strings.ContainsRune(part, 0) {
			return "", configuration("SQL identifiers must contain 1–63 UTF-8 bytes without NUL")
		}
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(parts, "."), nil
}

func boundType(schema hff.Type, path []string) (hff.Type, error) {
	typ := schema
	for index, name := range path {
		// Flattened columns cannot recover a nullable parent's identity or
		// distinguish an absent parent from a null leaf.
		if typ.Kind() != hff.StructType {
			return hff.Type{}, fmt.Errorf("binding traverses a non-required struct at %q", name)
		}
		found := false
		for _, field := range typ.Fields() {
			if field.Name == name {
				typ = field.Type
				found = true
				if field.Optional && index < len(path)-1 {
					return hff.Type{}, fmt.Errorf("binding traverses an optional parent at %q", name)
				}
				break
			}
		}
		if !found {
			return hff.Type{}, fmt.Errorf("binding path contains undeclared field %q", name)
		}
	}
	return typ, nil
}
