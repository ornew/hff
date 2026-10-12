package hff

import (
	"time"

	"github.com/ornew/hff/internal/core"
)

// Kind identifies a value's exact representation. InvalidKind is not null:
// use Null to create explicit HFF null, and use input validation to reject an
// invalid zero Value.
type Kind = core.Kind

// Value owns immutable data. Its zero value is invalid; it is neither missing
// nor null. Use Null for explicit null, and use Object lookup to distinguish a
// missing key from a key holding Null.
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

// Bool constructs a Boolean HFF value. The payload is stored exactly; HFF
// truthiness is defined by evaluation, not by this constructor.
func Bool(v bool) Value { return Value(core.Bool(v)) }

// Int constructs an int64 HFF value without conversion to or from float64.
func Int(v int64) Value { return Value(core.Int(v)) }

// Float constructs a float64 HFF value. Non-finite values are rejected by
// input validation rather than converted to null.
func Float(v float64) Value { return Value(core.Float(v)) }

// String constructs an HFF string without normalization or numeric parsing.
// Input validation rejects strings that are not valid UTF-8.
func String(v string) Value { return Value(core.String(v)) }

// Bytes copies v into an immutable byte value. Later changes to v do not
// change the Value; AsBytes returns another copy.
func Bytes(v []byte) Value { return Value(core.Bytes(v)) }

// List copies values into an immutable ordered list. The empty call creates an
// empty list, not null. Backends such as local Match validate it against the
// Program schema; ValidateInput can create a reusable checked input explicitly.
func List(values ...Value) Value {
	out := make([]core.Value, len(values))
	for i, v := range values {
		out[i] = core.Value(v)
	}
	return Value(core.OwnedList(out))
}

// Object copies values into an immutable object. A nil or empty map creates an
// empty object, not null. A missing key differs from a key explicitly mapped
// to Null(); use Lookup's bool result to tell them apart.
func Object(values map[string]Value) Value {
	out := make(map[string]core.Value, len(values))
	for k, v := range values {
		out[k] = core.Value(v)
	}
	return Value(core.OwnedObject(out))
}

// Timestamp constructs a timestamp normalized to UTC. It represents an
// instant; the input time's location and monotonic clock reading are not part
// of the HFF value.
func Timestamp(v time.Time) Value { return Value(core.Timestamp(v)) }

// Duration constructs a nanosecond-precision duration value.
func Duration(v time.Duration) Value { return Value(core.Duration(v)) }

// Kind returns the exact representation kind. The zero Value reports
// InvalidKind; explicit null reports NullKind.
func (v Value) Kind() Kind { return core.Value(v).Kind() }

// AsBool returns the Boolean payload and true only for BoolKind. The bool in
// the second result reports the kind match, not the payload's truth value.
func (v Value) AsBool() (bool, bool) { return core.Value(v).AsBool() }

// AsInt returns the int64 payload and true only for IntKind. It does not
// convert FloatKind values.
func (v Value) AsInt() (int64, bool) { return core.Value(v).AsInt() }

// AsFloat returns the float64 payload and true only for FloatKind. Input
// validation rejects NaN and infinities; this accessor does not coerce IntKind.
func (v Value) AsFloat() (float64, bool) { return core.Value(v).AsFloat() }

// AsString returns the string payload and true only for StringKind. The bool
// reports the kind match; it is not a success flag for string contents.
func (v Value) AsString() (string, bool) { return core.Value(v).AsString() }

// AsTimestamp returns the UTC-normalized instant and true only for
// TimestampKind.
func (v Value) AsTimestamp() (time.Time, bool) { return core.Value(v).AsTimestamp() }

// AsDuration returns the nanosecond-precision duration and true only for
// DurationKind.
func (v Value) AsDuration() (time.Duration, bool) { return core.Value(v).AsDuration() }

// AsBytes returns a defensive copy and true only for BytesKind. Mutating the
// returned bytes cannot change v.
func (v Value) AsBytes() ([]byte, bool) { return core.Value(v).AsBytes() }

// AsList returns a defensive copy of the ordered elements and true only for
// ListKind. A valid empty list returns a non-nil empty slice and true; false
// means the receiver is not a list, not that the list is empty.
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

// AsObject returns a defensive copy of the entries and true only for
// ObjectKind. A valid empty object returns a non-nil empty map and true; false
// means the receiver is not an object.
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
// value returns (Null(), true); a missing key or non-object receiver returns
// (zero Value, false). The zero Value in the latter case is invalid, not null.
func (v Value) Lookup(key string) (Value, bool) {
	value, ok := core.Value(v).Lookup(key)
	return Value(value), ok
}
