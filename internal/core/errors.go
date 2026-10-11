package core

import "fmt"

// ErrorCode identifies a host-level failure, never a completed false match.
type ErrorCode string

const (
	ConfigurationError ErrorCode = "configuration"
	SyntaxError        ErrorCode = "syntax"
	TypeError          ErrorCode = "type"
	CapabilityError    ErrorCode = "capability"
	InputError         ErrorCode = "input"
	LimitError         ErrorCode = "limit"
	CancelledError     ErrorCode = "cancelled"
)

// Path is a sequence of literal names, not a dot-separated identifier.
type Path []string

// Error supports errors.As; Cause supports errors.Is for context cancellation.
type Error struct {
	Code         ErrorCode
	Path         Path
	Message      string
	Cause        error
	Span         *Span
	Line, Column int
}

func (e *Error) Error() string {
	if len(e.Path) > 0 {
		return fmt.Sprintf("hff %s at %q: %s", e.Code, []string(e.Path), e.Message)
	}
	if e.Span != nil {
		return fmt.Sprintf("hff %s at %d:%d: %s", e.Code, e.Line, e.Column, e.Message)
	}
	return fmt.Sprintf("hff %s: %s", e.Code, e.Message)
}
func (e *Error) Unwrap() error { return e.Cause }
