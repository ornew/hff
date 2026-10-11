package hff

import "github.com/ornew/hff/internal/core"

type ErrorCode = core.ErrorCode
type Path = core.Path
type Error = core.Error

const (
	ConfigurationError = core.ConfigurationError
	SyntaxError        = core.SyntaxError
	TypeError          = core.TypeError
	CapabilityError    = core.CapabilityError
	InputError         = core.InputError
	LimitError         = core.LimitError
	CancelledError     = core.CancelledError
)
