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

// Dialect identifies the SQL translation contract. Build requires an explicit
// dialect; this package currently implements PostgreSQL only.
type Dialect string

// PostgreSQL selects the PostgreSQL translation contract.
const PostgreSQL Dialect = "postgresql"

// ColumnRef is a sequence of literal SQL identifier components, never an SQL
// expression. Components are quoted separately by Build.
type ColumnRef struct{ parts []string }

// Column constructs a reference from literal identifier components, such as
// Column("orders", "tenant_id"). It does not accept SQL expressions.
func Column(parts ...string) ColumnRef { return ColumnRef{append([]string(nil), parts...)} }

// Binding maps one literal HFF schema path to a physical SQL representation.
// Construct bindings with Bind, BindTimestamp, BindJSON, BindComposite,
// BindStruct, or BindRelation, then pass them to Build through WithBindings.
type Binding struct {
	path                   hff.Path
	column                 ColumnRef
	format                 JSONFormat
	composite              bool
	structure              *structBinding
	relation               *Relation
	metadata               *ColumnRef
	timestamp              bool
	timestampRemainder     *ColumnRef
	invalidTimestampOption bool
	invalidJSONOption      bool
}

// Bind maps a scalar HFF path to a SQL column. Path components are literal
// field names, not dotted text; the caller guarantees column type and data
// satisfy the declared HFF schema. Duration values use bigint nanoseconds.
func Bind(path hff.Path, column ColumnRef) Binding {
	return Binding{path: append(hff.Path(nil), path...), column: Column(column.parts...)}
}

// Config is the SQL Build configuration. An explicit dialect is required;
// bindings must cover every schema path used by the Program. Method
// implementations add SQL translations for declared extension methods.
type Config struct {
	// Dialect is required and must currently be PostgreSQL.
	Dialect Dialect
	// Bindings maps the HFF schema paths used by the Program to SQL storage.
	Bindings []Binding
	// Methods contains SQL translators for declared extension methods.
	Methods []MethodImplementation
	// ExecutionLimits requests HFF runtime budgets; positive PostgreSQL requests
	// fail because the database translator cannot enforce them.
	ExecutionLimits ExecutionLimits
}

// Option changes one SQL Build setting. Options apply from left to right.
type Option struct{ apply func(*Config) error }

// WithDialect selects the required SQL dialect. PostgreSQL is currently the
// only supported dialect.
func WithDialect(dialect Dialect) Option {
	return Option{func(c *Config) error { c.Dialect = dialect; return nil }}
}

// WithBindings replaces the schema-to-column mappings used by Build. The slice
// is copied; each path and physical representation is explicit.
func WithBindings(bindings ...Binding) Option {
	return Option{func(c *Config) error { c.Bindings = append([]Binding(nil), bindings...); return nil }}
}

// WithConfig copies a complete SQL configuration. Nil is rejected, and later
// options replace settings copied from this value.
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

// Query owns a reusable PostgreSQL WHERE predicate and its ordered driver
// parameters. It performs no database I/O, chooses no tables, and does not
// execute a query; the caller integrates Where and Args into its own SQL.
type Query struct {
	where   string
	args    []any
	profile hff.Profile
}

// Where returns the generated Boolean SQL expression without a WHERE keyword.
// A nil Query returns an empty string.
func (q *Query) Where() string {
	if q == nil {
		return ""
	}
	return q.where
}

// Args returns a fresh, ordered slice of driver arguments corresponding to the
// placeholders in Where. Current encodings are immutable scalars.
func (q *Query) Args() []any {
	if q == nil {
		return nil
	}
	return append([]any(nil), q.args...)
}

// Build checks whether PostgreSQL can preserve the Program's supported
// semantics and translates it to a reusable Query. It performs no database
// access or record validation. Query values remain bound arguments rather than
// interpolated SQL. Options apply left to right; unsupported operations return
// source-located BackendErrors.
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
	b := builder{methods: methods, ctx: ctx, program: program, bindings: make(map[string]string), reserved: make(map[string]bool), jsonBindings: make(map[string]JSONFormat), jsonValues: make(map[string]JSONFormat), compositeBindings: make(map[string]bool), structBindings: make(map[string]Binding), relationBindings: make(map[string]Binding)}
	if err := b.checkMethods(program.Root()); err != nil {
		return nil, err
	}
	b.physicalBindings = append([]Binding(nil), config.Bindings...)
	for _, binding := range config.Bindings {
		b.reserveBindingIdentifiers(binding)
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
		if binding.timestamp {
			if !timestampSchema(typ) || binding.format != "" || binding.composite || binding.structure != nil || binding.relation != nil || binding.metadata != nil {
				return nil, configuration("timestamp binding requires one Timestamp field and native columns")
			}
			code, err := b.timestampBindingCode(binding, columnFormats, columnKinds)
			if err != nil {
				return nil, err
			}
			b.bindings[key] = code
			continue
		}
		if timestampSchema(typ) && binding.format == "" {
			return nil, configuration("Timestamp fields require BindTimestamp to declare their precision contract")
		}
		if binding.relation != nil {
			if err := b.checkRelation(binding, typ, columnKinds, columnFormats, columnJSONTypes); err != nil {
				return nil, err
			}
			b.bindings[key] = ""
			b.jsonBindings[key] = JSON
			b.relationBindings[key] = binding
			continue
		}
		if binding.structure != nil {
			if err := b.checkStructBinding(binding, typ, columnKinds, columnFormats, columnJSONTypes); err != nil {
				return nil, err
			}
			b.bindings[key] = ""
			b.jsonBindings[key] = JSON
			b.structBindings[key] = binding
			continue
		}
		metadata, err := b.checkJSONMetadata(binding)
		if err != nil {
			return nil, err
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
		if metadata != "" {
			b.bindings[key] = b.restoreJSONMetadata(column, metadata)
			b.jsonBindings[key] = JSON
		}
		for _, component := range binding.column.parts {
			b.reserved[component] = true
		}
	}
	for _, parent := range config.Bindings {
		if parent.format == "" && !parent.composite && parent.structure == nil && parent.relation == nil {
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
	if err := b.checkRelationOrder(program.Root(), hff.Node{}, 0); err != nil {
		return nil, err
	}
	where, err := b.filterWhere(program.Root())
	if err != nil {
		return nil, err
	}
	if b.predicateFailure != nil {
		return nil, b.predicateFailure
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
