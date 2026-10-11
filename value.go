// Package hff defines immutable values and shared contracts for Human-Friendly Filter.
package hff

import "github.com/ornew/hff/internal/core"
import "time"

// Kind identifies a value's exact representation. InvalidKind is not null.
type Kind = core.Kind

// Value owns immutable data. The zero value is invalid; use Null for null.
type Value core.Value

const (
	InvalidKind   = core.InvalidKind
	NullKind      = core.NullKind
	BoolKind      = core.BoolKind
	IntKind       = core.IntKind
	FloatKind     = core.FloatKind
	StringKind    = core.StringKind
	BytesKind     = core.BytesKind
	ListKind      = core.ListKind
	ObjectKind    = core.ObjectKind
	TimestampKind = core.TimestampKind
	DurationKind  = core.DurationKind
)

func Null() Value           { return Value(core.Null()) }
func Bool(v bool) Value     { return Value(core.Bool(v)) }
func Int(v int64) Value     { return Value(core.Int(v)) }
func Float(v float64) Value { return Value(core.Float(v)) }
func String(v string) Value { return Value(core.String(v)) }
func Bytes(v []byte) Value  { return Value(core.Bytes(v)) }
func List(values ...Value) Value {
	out := make([]core.Value, len(values))
	for i, v := range values {
		out[i] = core.Value(v)
	}
	return Value(core.OwnedList(out))
}
func Object(values map[string]Value) Value {
	out := make(map[string]core.Value, len(values))
	for k, v := range values {
		out[k] = core.Value(v)
	}
	return Value(core.OwnedObject(out))
}
func Timestamp(v time.Time) Value    { return Value(core.Timestamp(v)) }
func Duration(v time.Duration) Value { return Value(core.Duration(v)) }

func (v Value) Kind() Kind                        { return core.Value(v).Kind() }
func (v Value) AsBool() (bool, bool)              { return core.Value(v).AsBool() }
func (v Value) AsInt() (int64, bool)              { return core.Value(v).AsInt() }
func (v Value) AsFloat() (float64, bool)          { return core.Value(v).AsFloat() }
func (v Value) AsString() (string, bool)          { return core.Value(v).AsString() }
func (v Value) AsTimestamp() (time.Time, bool)    { return core.Value(v).AsTimestamp() }
func (v Value) AsDuration() (time.Duration, bool) { return core.Value(v).AsDuration() }
func (v Value) AsBytes() ([]byte, bool)           { return core.Value(v).AsBytes() }
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
func (v Value) Lookup(key string) (Value, bool) {
	value, ok := core.Value(v).Lookup(key)
	return Value(value), ok
}
