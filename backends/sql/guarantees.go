package sql

import "github.com/ornew/hff"

// ExecutionLimits requests per-record HFF evaluation budgets from a backend.
// Zero requests no guarantee; negative values are invalid. These are HFF
// budgets, not PostgreSQL statement timeouts.
type ExecutionLimits struct {
	// MaxSteps requests a maximum HFF runtime work count.
	MaxSteps int
	// MaxGeneratedValues requests a maximum count of generated HFF values.
	MaxGeneratedValues int
}

// Guarantees reports whether a backend can enforce the requested HFF budgets.
type Guarantees struct {
	// MaxSteps reports enforcement of per-record HFF runtime steps.
	MaxSteps bool
	// MaxGeneratedValues reports enforcement of generated HFF value count.
	MaxGeneratedValues bool
}

// ExecutionGuarantees reports runtime-budget support for a dialect without
// building a particular Program or contacting a database.
func ExecutionGuarantees(dialect Dialect) (Guarantees, error) {
	if dialect != PostgreSQL {
		return Guarantees{}, configuration("explicit supported SQL dialect required")
	}
	return Guarantees{}, nil
}

// Guarantees reports budgets enforceable by this Query's backend. PostgreSQL
// currently reports neither HFF step nor generated-value budget enforcement.
func (q *Query) Guarantees() Guarantees { return Guarantees{} }

// WithExecutionLimits requests backend-enforced HFF runtime budgets during SQL
// execution. PostgreSQL rejects positive requests because it cannot enforce
// them; configure database execution limits through the caller instead.
func WithExecutionLimits(limits ExecutionLimits) Option {
	return Option{func(c *Config) error { c.ExecutionLimits = limits; return nil }}
}
func checkExecutionLimits(limits ExecutionLimits) error {
	if limits.MaxSteps < 0 || limits.MaxGeneratedValues < 0 {
		return configuration("execution budgets cannot be negative")
	}
	if limits.MaxSteps > 0 || limits.MaxGeneratedValues > 0 {
		return &hff.Error{Code: hff.BackendError, Message: "PostgreSQL cannot enforce HFF execution budgets; configure database execution limits through the caller"}
	}
	return nil
}
