package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrorCode identifies a host-level failure, never a completed false match.
type ErrorCode string

const (
	ConfigurationError ErrorCode = "configuration"
	SyntaxError        ErrorCode = "syntax"
	TypeError          ErrorCode = "type"
	CapabilityError    ErrorCode = "capability"
	BackendError       ErrorCode = "backend"
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

// Diagnostics groups independent failures. Unwrap preserves errors.As for each
// typed Error and errors.Is for causes. It never represents a usable program.
type Diagnostics struct{ items []error }

func (d *Diagnostics) Errors() []error {
	if d == nil {
		return nil
	}
	return append([]error(nil), d.items...)
}
func (d *Diagnostics) Unwrap() []error { return d.Errors() }
func (d *Diagnostics) Error() string {
	if d == nil {
		return ""
	}
	messages := make([]string, len(d.items))
	for i, err := range d.items {
		messages[i] = err.Error()
	}
	return strings.Join(messages, "\n")
}

// CombineErrors retains a single failure's existing API and flattens groups
// when recovery has found more than one independent diagnostic.
func CombineErrors(errs ...error) error {
	var items []error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if group, ok := err.(*Diagnostics); ok {
			items = append(items, group.items...)
		} else {
			items = append(items, err)
		}
	}
	if len(items) == 0 {
		return nil
	}
	if len(items) == 1 {
		return items[0]
	}
	return &Diagnostics{items: items}
}

// LocateSourceErrors fills source positions in one UTF-8 scan. Collecting many
// diagnostics must not repeatedly scan the entire prefix for every error.
func LocateSourceErrors(ctx context.Context, source string, err error) error {
	if err == nil {
		return nil
	}
	items := []error{err}
	if group, ok := err.(*Diagnostics); ok {
		items = group.Errors()
	}
	var locations []*Error
	for _, item := range items {
		var detail *Error
		if errors.As(item, &detail) && detail.Span != nil && detail.Line == 0 {
			locations = append(locations, detail)
		}
	}
	sort.SliceStable(locations, func(i, j int) bool { return locations[i].Span.Start < locations[j].Span.Start })
	line, column, next, nextCheck := 1, 1, 0, 0
	for pos, r := range source {
		if pos >= nextCheck {
			if contextErr := CheckContext(ctx); contextErr != nil {
				return contextErr
			}
			nextCheck = pos + 1024
		}
		for next < len(locations) && locations[next].Span.Start <= pos {
			locations[next].Line, locations[next].Column = line, column
			next++
		}
		if next == len(locations) {
			return err
		}
		if r == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	for ; next < len(locations); next++ {
		locations[next].Line, locations[next].Column = line, column
	}
	return err
}

// CheckContext preserves both the standard cancellation status and an optional
// host-supplied cause. Callers validate a non-nil context before reaching here.
func CheckContext(ctx context.Context) error {
	if status := ctx.Err(); status != nil {
		cause := error(status)
		if custom := context.Cause(ctx); custom != nil && custom != status {
			cause = errors.Join(status, custom)
		}
		return &Error{Code: CancelledError, Message: status.Error(), Cause: cause}
	}
	return nil
}
