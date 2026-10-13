package sql

import (
	"math/big"
	"time"

	"github.com/ornew/hff"
)

// TimestampBindingOption configures a native PostgreSQL Timestamp binding.
// Options apply from left to right.
type TimestampBindingOption struct{ apply func(*Binding) }

// WithNanosecondRemainder identifies the SMALLINT column containing the
// sub-microsecond remainder, from 0 through 999 nanoseconds. The caller stores
// the timestamptz rounded down to a whole microsecond and guarantees that
// both columns are null together. Without this option, the caller guarantees
// that every stored instant is already exact to a microsecond.
func WithNanosecondRemainder(column ColumnRef) TimestampBindingOption {
	return TimestampBindingOption{apply: func(binding *Binding) {
		owned := Column(column.parts...)
		binding.timestampRemainder = &owned
	}}
}

// BindTimestamp maps one schema-known Timestamp field to timestamptz storage.
// It may also be a relative BindStruct child or a BindRelation item for direct
// Timestamp predicates. Materializing a whole container containing Timestamp
// remains unsupported. HFF's year 0000 maps to PostgreSQL 1 BC. The caller
// guarantees stored values are finite, within HFF's years 0000–9999, and
// satisfy the declared schema. This binding never rounds a query value.
func BindTimestamp(path hff.Path, column ColumnRef, options ...TimestampBindingOption) Binding {
	binding := Bind(path, column)
	binding.timestamp = true
	for _, option := range options {
		if option.apply == nil {
			binding.invalidTimestampOption = true
			continue
		}
		option.apply(&binding)
	}
	return binding
}

func timestampSchema(typ hff.Type) bool {
	if typ.Kind() == hff.TimestampType {
		return true
	}
	if typ.Kind() != hff.UnionType {
		return false
	}
	members := typ.Members()
	if len(members) != 2 {
		return false
	}
	return members[0].Kind() == hff.NullType && members[1].Kind() == hff.TimestampType ||
		members[1].Kind() == hff.NullType && members[0].Kind() == hff.TimestampType
}

func (b *builder) timestampBindingCode(binding Binding, formats map[string]JSONFormat, kinds map[string]hff.TypeKind) (string, error) {
	if binding.invalidTimestampOption {
		return "", configuration("invalid TimestampBindingOption")
	}
	column, err := quoteColumn(binding.column)
	if err != nil {
		return "", err
	}
	if previous, exists := formats[column]; exists && previous != "timestamp" {
		return "", configuration("SQL column has conflicting physical formats")
	}
	if previous, exists := kinds[column]; exists && previous != hff.TimestampType {
		return "", configuration("SQL column has conflicting schema types")
	}
	formats[column], kinds[column] = "timestamp", hff.TimestampType
	if binding.timestampRemainder != nil {
		remainder, err := quoteColumn(*binding.timestampRemainder)
		if err != nil {
			return "", err
		}
		if remainder == column {
			return "", configuration("Timestamp remainder must use a separate column")
		}
		if _, exists := formats[remainder]; exists {
			return "", configuration("Timestamp remainder column conflicts with another binding")
		}
		formats[remainder] = "timestamp.remainder"
	}
	return timestampBindingExpression(binding)
}

// timestampBindingExpression is used after the binding's physical columns
// have been validated in its enclosing top-level, struct, or relation scope.
func timestampBindingExpression(binding Binding) (string, error) {
	column, err := quoteColumn(binding.column)
	if err != nil {
		return "", err
	}
	code := "(EXTRACT(EPOCH FROM " + column + ")::numeric * 1000000000::numeric)"
	if binding.timestampRemainder != nil {
		remainder, err := quoteColumn(*binding.timestampRemainder)
		if err != nil {
			return "", err
		}
		code = "(" + code + " + " + remainder + "::numeric)"
	}
	return code, nil
}

func timestampNanoseconds(stamp time.Time) string {
	seconds := new(big.Int).SetInt64(stamp.Unix())
	seconds.Mul(seconds, big.NewInt(1000000000))
	seconds.Add(seconds, big.NewInt(int64(stamp.Nanosecond())))
	return seconds.String()
}
