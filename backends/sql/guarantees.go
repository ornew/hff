package sql

import "github.com/ornew/hff"

// ExecutionLimits requests per-record HFF evaluation budgets. Zero requests no
// guarantee; negative values are invalid. These are not database timeouts.
type ExecutionLimits struct{ MaxSteps, MaxGeneratedValues int }

// Guarantees describes budgets a dialect can enforce during database execution.
// A false field means Build rejects a positive request for that budget.
type Guarantees struct{ MaxSteps, MaxGeneratedValues bool }

func ExecutionGuarantees(dialect Dialect) (Guarantees, error) {
	if dialect != PostgreSQL {
		return Guarantees{}, configuration("explicit supported SQL dialect required")
	}
	return Guarantees{}, nil
}
func (q *Query) Guarantees() Guarantees { return Guarantees{} }
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
