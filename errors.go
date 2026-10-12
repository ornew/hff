package hff

import "github.com/ornew/hff/internal/core"

// ErrorCode classifies a configuration, syntax, type, capability, backend,
// input, limit, or cancellation failure. A completed false match is not an error.
type ErrorCode = core.ErrorCode

// Path is a sequence of literal field or index labels. Components are not
// split on dots; for example, []string{"service.name"} is one field.
type Path = core.Path

// Error is the typed failure returned by the API. Inspect Code rather than
// parsing Message text. Path identifies an input location; Span, Line, and
// Column identify query source when available. Span is a half-open byte range;
// Line and Column are one-based, with columns counted in Unicode code points.
// Cause supports errors.Is.
type Error = core.Error

// Diagnostics groups independent source errors. Use errors.As for individual
// *Error values and Errors to obtain a defensive copy of the members.
type Diagnostics = core.Diagnostics

const (
	// ConfigurationError indicates invalid host configuration or API arguments.
	ConfigurationError = core.ConfigurationError
	// SyntaxError indicates a malformed HFF query.
	SyntaxError = core.SyntaxError
	// TypeError indicates a query value or operation violates the schema types.
	TypeError = core.TypeError
	// CapabilityError indicates the query requires a denied or missing permission.
	CapabilityError = core.CapabilityError
	// BackendError indicates a backend cannot implement a valid compiled program.
	BackendError = core.BackendError
	// InputError indicates a record is invalid or incompatible with its schema.
	InputError = core.InputError
	// LimitError indicates a configured finite resource limit was exceeded.
	LimitError = core.LimitError
	// CancelledError indicates a context cancellation or deadline.
	CancelledError = core.CancelledError
)
