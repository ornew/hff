package hff

import "github.com/ornew/hff/internal/core"

type ErrorCode = core.ErrorCode
type Path = core.Path
type Error = core.Error
type Diagnostics = core.Diagnostics

const (
	ConfigurationError = core.ConfigurationError
	SyntaxError        = core.SyntaxError
	TypeError          = core.TypeError
	CapabilityError    = core.CapabilityError
	BackendError       = core.BackendError
	InputError         = core.InputError
	LimitError         = core.LimitError
	CancelledError     = core.CancelledError
)
