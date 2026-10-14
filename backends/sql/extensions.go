package sql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ornew/hff"
)

// MethodFunc translates one declared extension during Build. The function must
// be deterministic, side-effect-free, safe for concurrent Builds, and preserve
// the declaration's semantics on the domain it accepts. Return an error when
// faithful translation is unavailable. SQL execution remains caller-owned.
type MethodFunc func(call *Call, receiver Expression, args []Expression) (Expression, error)

// MethodImplementation associates an extension declaration with one dialect
// translator. The translator is invoked during Build, never for each database
// row.
type MethodImplementation struct {
	declaration hff.MethodDeclaration
	dialect     Dialect
	function    MethodFunc
	identity    *methodIdentity
}

type methodIdentity struct{ marker byte }

// Declaration returns the immutable signature this translator implements.
// It does not expose or invoke the executable translation function.
func (m MethodImplementation) Declaration() hff.MethodDeclaration { return m.declaration }

// Dialect returns the dialect selected when this translator was registered.
func (m MethodImplementation) Dialect() Dialect { return m.dialect }

// Valid reports whether the translator has a declaration and an executable
// function. A zero implementation or one made with a nil function is invalid.
func (m MethodImplementation) Valid() bool {
	return m.declaration.Valid() && m.dialect != "" && m.function != nil && m.identity != nil
}

// SameRegistration reports whether two values came from the same Implement
// call, including copies of that immutable registration. Equal signatures do
// not prove that independently registered functions have equal behavior.
func (m MethodImplementation) SameRegistration(other MethodImplementation) bool {
	return m.Valid() && other.Valid() && m.identity == other.identity
}

// Implement registers a translator for declaration under dialect. Build checks
// that the declaration exactly matches the one compiled into the Program.
func Implement(declaration hff.MethodDeclaration, dialect Dialect, function MethodFunc) MethodImplementation {
	return MethodImplementation{declaration: declaration, dialect: dialect, function: function, identity: &methodIdentity{}}
}

// WithMethods replaces the translators configured for SQL Build. The slice is
// copied; implementations must be safe for concurrent Build calls.
func WithMethods(methods ...MethodImplementation) Option {
	owned := append([]MethodImplementation(nil), methods...)
	return Option{func(c *Config) error { c.Methods = append([]MethodImplementation(nil), owned...); return nil }}
}

// Expression is an immutable typed scalar SQL value scoped to one translation
// Call. Its zero value is invalid. Raw SQL and internal presence flags are not
// exposed; the declared type does not prove the SQL expression's behavior.
type Expression struct {
	owner *Call
	value expression
	typ   hff.Type
}

// Valid reports whether e has a translator owner and a declared type. It does
// not extend its lifetime: expressions may only be used during their own Call.
func (e Expression) Valid() bool { return e.owner != nil && e.typ.Kind() != hff.InvalidType }

// Type returns the declared HFF scalar type of e.
func (e Expression) Type() hff.Type { return e.typ }

// Call provides the selected dialect, context, and constructors to a translator.
// Helpers may be called concurrently while translation is active; retaining
// Call or its expressions for later work is invalid.
type Call struct {
	mu      sync.Mutex
	ctx     context.Context
	dialect Dialect
	builder *builder
	node    hff.Node
	failure error
}

// Context returns the Build context for this translation, or nil for a nil Call.
func (c *Call) Context() context.Context {
	if c == nil {
		return nil
	}
	return c.ctx
}

// Dialect reports the dialect for this translation.
func (c *Call) Dialect() Dialect {
	if c == nil {
		return ""
	}
	return c.dialect
}
func (c *Call) fail(message string) error {
	err := c.builder.failure(c.node, message)
	c.failure = err
	return err
}
func (c *Call) active() error {
	if c.builder == nil {
		return &hff.Error{Code: hff.BackendError, Message: "SQL extension Call is no longer active"}
	}
	if c.failure != nil {
		return c.failure
	}
	if err := checkContext(c.ctx); err != nil {
		c.failure = err
		return err
	}
	return nil
}

// Parameter binds a supported HFF scalar using the dialect's validated driver
// encoding and returns an expression scoped to this Call. PostgreSQL accepts
// null, bool, int, finite float, string, and bytes. Bytes are bound as immutable
// hexadecimal text and decoded to bytea; Query.Args never exposes a mutable
// byte slice. Strings must be valid UTF-8 without NUL.
func (c *Call) Parameter(value hff.Value) (Expression, error) {
	if c == nil {
		return Expression{}, configuration("nil SQL extension Call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.active(); err != nil {
		return Expression{}, err
	}
	v, err := c.builder.parameter(c.node, value)
	if err != nil {
		c.failure = err
		return Expression{}, err
	}
	return Expression{c, v, hff.PrimitiveType(v.kind)}, nil
}

// Expression fills each {} in a fixed developer-authored SQL template with a
// scoped Expression and declares the result's HFF scalar type. Never put query
// text or record data in the template: use Parameter for values. Templates must
// not contain $N parameters. HFF checks ownership, placeholder count, and type
// shape, but does not parse or prove SQL semantics, purity, or database
// function behavior; the translator author owns those contracts.
func (c *Call) Expression(typ hff.Type, template string, args ...Expression) (Expression, error) {
	if c == nil {
		return Expression{}, configuration("nil SQL extension Call")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.active(); err != nil {
		return Expression{}, err
	}
	kind, ok := scalarKind(typ)
	if typ.Check() != nil || !ok {
		return Expression{}, c.fail("SQL extension expression requires one supported scalar type")
	}
	if strings.TrimSpace(template) == "" || !utf8.ValidString(template) || strings.ContainsRune(template, 0) {
		return Expression{}, c.fail("invalid SQL extension template")
	}
	for i := 0; i+1 < len(template); i++ {
		if template[i] == '$' && template[i+1] >= '0' && template[i+1] <= '9' {
			return Expression{}, c.fail("SQL extension template must use bound expressions instead of $N")
		}
	}
	parts := strings.Split(template, "{}")
	if len(parts) != len(args)+1 {
		return Expression{}, c.fail("SQL extension template placeholder count differs from argument count")
	}
	var code strings.Builder
	code.WriteString(parts[0])
	for i, arg := range args {
		if !arg.Valid() || arg.owner != c {
			return Expression{}, c.fail("SQL extension expression belongs to another Call or is invalid")
		}
		code.WriteString(arg.value.code)
		code.WriteString(parts[i+1])
	}
	return Expression{c, expression{"(" + code.String() + ")", "TRUE", kind}, typ}, nil
}
func snapshotMethods(methods []MethodImplementation, dialect Dialect) (map[string]MethodImplementation, error) {
	out := make(map[string]MethodImplementation)
	for _, m := range methods {
		if !m.declaration.Valid() || m.function == nil || m.dialect != dialect {
			return nil, configuration("invalid SQL method implementation or incompatible dialect")
		}
		if _, ok := out[m.declaration.ID()]; ok {
			return nil, configuration("duplicate SQL method implementation ID " + m.declaration.ID())
		}
		out[m.declaration.ID()] = m
	}
	return out, nil
}
func (b *builder) checkMethods(n hff.Node) error {
	if err := checkContext(b.ctx); err != nil {
		return err
	}
	if method, ok := n.Method(); ok {
		for _, s := range method.Signatures() {
			if d, custom := s.Declaration(); custom {
				m, exists := b.methods[d.ID()]
				if !exists || !m.declaration.Equal(d) {
					return b.failure(n, "missing or incompatible SQL method implementation "+d.ID())
				}
			}
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if err := b.checkMethods(child); err != nil {
			return err
		}
	}
	return nil
}
func typeCondition(value expression, typ hff.Type, policy hff.NullPolicy) string {
	nonnull := acceptsKind(typ, value.kind) && value.kind != hff.NullType
	nullable := policy == hff.InvokeNull && acceptsKind(typ, hff.NullType)
	if nonnull && nullable {
		return "TRUE"
	}
	if nonnull {
		return "(" + value.code + " IS NOT NULL)"
	}
	if nullable {
		return "(" + value.code + " IS NULL)"
	}
	return "FALSE"
}
func (b *builder) extension(n hff.Node, method hff.Method) (expression, error) {
	resultKind, ok := scalarKind(n.Type())
	if !ok {
		return expression{}, b.failure(n, "SQL extension result requires one supported scalar type")
	}
	values := make([]expression, n.ChildCount())
	for i := range values {
		child, _ := n.Child(i)
		v, err := b.expression(child)
		if err != nil {
			return expression{}, err
		}
		values[i] = v
	}
	signatures := method.Signatures()
	prefixes := make([]string, len(signatures))
	for i := range prefixes {
		prefixes[i] = "TRUE"
	}
	present := "TRUE"
	for i, value := range values {
		reached := "(" + strings.Join(prefixes, " OR ") + ")"
		present = both(present, "(NOT "+reached+" OR "+value.present+")")
		for j, s := range signatures {
			d, _ := s.Declaration()
			want := d.Receiver()
			if i > 0 {
				want, _ = d.Parameter(i - 1)
			}
			prefixes[j] = both(prefixes[j], typeCondition(value, want, d.NullPolicy()))
		}
	}
	var branches []string
	for i, s := range signatures {
		d, _ := s.Declaration()
		call := &Call{ctx: b.ctx, dialect: PostgreSQL, builder: b, node: n}
		wrap := func(index int) Expression {
			child, _ := n.Child(index)
			return Expression{call, values[index], child.Type()}
		}
		args := make([]Expression, len(values)-1)
		for j := range args {
			args[j] = wrap(j + 1)
		}
		value, err := invokeTranslator(b.methods[d.ID()].function, call, wrap(0), args)
		call.mu.Lock()
		if call.failure != nil {
			err = call.failure
		}
		call.builder = nil
		call.mu.Unlock()
		contextErr := checkContext(b.ctx)
		if err == nil {
			err = contextErr
		} else if contextErr != nil && (errors.Is(err, b.ctx.Err()) || errors.Is(err, context.Cause(b.ctx))) {
			err = errors.Join(contextErr, err)
		}
		if err != nil {
			return expression{}, b.translationFailure(n, err)
		}
		if !value.Valid() || value.owner != call || (value.value.kind != hff.NullType && (!acceptsKind(d.Result(), value.value.kind) || value.value.kind != resultKind)) {
			return expression{}, b.failure(n, "SQL extension returned an invalid, foreign, or incompatible expression")
		}
		branches = append(branches, "WHEN "+both(present, prefixes[i])+" THEN "+value.value.code)
	}
	code := "NULL"
	if len(branches) > 0 {
		code = "(CASE " + strings.Join(branches, " ") + " ELSE NULL END)"
	}
	if resultKind != hff.NullType {
		code = "(" + code + ")::" + sqlType(resultKind)
	}
	return expression{code, present, resultKind}, nil
}
func invokeTranslator(fn MethodFunc, c *Call, receiver Expression, args []Expression) (result Expression, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			cause, ok := failure.(error)
			if !ok {
				cause = fmt.Errorf("%v", failure)
			}
			err = fmt.Errorf("SQL extension translator panicked: %w", cause)
		}
	}()
	return fn(c, receiver, args)
}
func (b *builder) translationFailure(n hff.Node, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &hff.Error{Code: hff.CancelledError, Message: err.Error(), Cause: err}
	}
	var detail *hff.Error
	if errors.As(err, &detail) && detail.Code == hff.CancelledError {
		return err
	}
	result := b.failure(n, err.Error()).(*hff.Error)
	result.Cause = err
	return result
}

func sqlType(kind hff.TypeKind) string {
	switch kind {
	case hff.BoolType:
		return "boolean"
	case hff.IntType, hff.DurationType:
		return "bigint"
	case hff.FloatType:
		return "double precision"
	case hff.StringType:
		return "text"
	case hff.BytesType:
		return "bytea"
	}
	return "text"
}
