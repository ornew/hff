// Package hff defines immutable HFF values and shared public contracts for
// Human-Friendly Filter, including schemas, inputs, compiled plans, limits,
// errors, profiles, and extension declarations. The engine package handles
// compilation and validation; backend packages build Programs for evaluation.
package hff

import (
	"time"

	"github.com/ornew/hff/internal/core"
)

// Kind identifies a value's exact representation. InvalidKind is not null.
type Kind = core.Kind

// Value owns immutable data. The zero value is invalid; use Null for null.
type Value core.Value

const (
	// InvalidKind is the invalid zero Value representation.
	InvalidKind = core.InvalidKind
	// NullKind is explicit HFF null.
	NullKind = core.NullKind
	// BoolKind is a Boolean value.
	BoolKind = core.BoolKind
	// IntKind is a signed int64 value.
	IntKind = core.IntKind
	// FloatKind is a finite float64 value.
	FloatKind = core.FloatKind
	// StringKind is a valid UTF-8 string value.
	StringKind = core.StringKind
	// BytesKind is a byte sequence.
	BytesKind = core.BytesKind
	// ListKind is an ordered list of HFF values.
	ListKind = core.ListKind
	// ObjectKind is a string-keyed object of HFF values.
	ObjectKind = core.ObjectKind
	// TimestampKind is a timestamp value.
	TimestampKind = core.TimestampKind
	// DurationKind is a duration value.
	DurationKind = core.DurationKind
)

// Null constructs the explicit HFF null value.
func Null() Value { return Value(core.Null()) }

// Bool constructs a Boolean HFF value.
func Bool(v bool) Value { return Value(core.Bool(v)) }

// Int constructs an int64 HFF value without numeric conversion.
func Int(v int64) Value { return Value(core.Int(v)) }

// Float constructs a float64 HFF value. Non-finite values are rejected by
// input validation rather than converted to null.
func Float(v float64) Value { return Value(core.Float(v)) }

// String constructs an HFF string. Input validation checks UTF-8 validity.
func String(v string) Value { return Value(core.String(v)) }

// Bytes copies its input into an immutable byte value.
func Bytes(v []byte) Value { return Value(core.Bytes(v)) }

// List copies values into an immutable ordered list.
func List(values ...Value) Value {
	out := make([]core.Value, len(values))
	for i, v := range values {
		out[i] = core.Value(v)
	}
	return Value(core.OwnedList(out))
}

// Object copies values into an immutable object. A missing key differs from a
// key explicitly mapped to Null().
func Object(values map[string]Value) Value {
	out := make(map[string]core.Value, len(values))
	for k, v := range values {
		out[k] = core.Value(v)
	}
	return Value(core.OwnedObject(out))
}

// Timestamp constructs a UTC-normalized timestamp value.
func Timestamp(v time.Time) Value { return Value(core.Timestamp(v)) }

// Duration constructs a nanosecond-precision duration value.
func Duration(v time.Duration) Value { return Value(core.Duration(v)) }

// Kind returns the exact representation kind; the zero Value is InvalidKind.
func (v Value) Kind() Kind { return core.Value(v).Kind() }

// AsBool returns the Boolean value and true only when v is BoolKind.
func (v Value) AsBool() (bool, bool) { return core.Value(v).AsBool() }

// AsInt returns the int64 value and true only when v is IntKind.
func (v Value) AsInt() (int64, bool) { return core.Value(v).AsInt() }

// AsFloat returns the float64 value and true only when v is FloatKind.
func (v Value) AsFloat() (float64, bool) { return core.Value(v).AsFloat() }

// AsString returns the string value and true only when v is StringKind.
func (v Value) AsString() (string, bool) { return core.Value(v).AsString() }

// AsTimestamp returns the instant and true only when v is TimestampKind.
func (v Value) AsTimestamp() (time.Time, bool) { return core.Value(v).AsTimestamp() }

// AsDuration returns the duration and true only when v is DurationKind.
func (v Value) AsDuration() (time.Duration, bool) { return core.Value(v).AsDuration() }

// AsBytes returns a defensive copy of bytes and true only for BytesKind.
func (v Value) AsBytes() ([]byte, bool) { return core.Value(v).AsBytes() }

// AsList returns a defensive copy of the ordered elements and a kind flag.
func (v Value) AsList() ([]Value, bool) {
	values, ok := core.Value(v).ListView()
	if !ok {
		return nil, false
	}
	out := make([]Value, len(values))
	for i, x := range values {
		out[i] = Value(x)
	}
	return out, true
}

// AsObject returns a defensive copy of the object's entries and a kind flag.
func (v Value) AsObject() (map[string]Value, bool) {
	values, ok := core.Value(v).ObjectView()
	if !ok {
		return nil, false
	}
	out := make(map[string]Value, len(values))
	for k, x := range values {
		out[k] = Value(x)
	}
	return out, true
}

// Lookup returns an object field and whether the key exists. A present Null()
// value returns true; an absent key returns false.
func (v Value) Lookup(key string) (Value, bool) {
	value, ok := core.Value(v).Lookup(key)
	return Value(value), ok
}
