// Package core owns HFF's immutable value and type representations.
package core

import "time"

// Kind identifies a value's exact representation. InvalidKind is not null.
type Kind uint8

const (
	InvalidKind Kind = iota
	NullKind
	BoolKind
	IntKind
	FloatKind
	StringKind
	BytesKind
	ListKind
	ObjectKind
	TimestampKind
	DurationKind
)

func (k Kind) String() string {
	names := [...]string{"invalid", "null", "bool", "int64", "float64", "string", "bytes", "list", "object", "timestamp", "duration"}
	if int(k) >= len(names) {
		return "invalid"
	}
	return names[k]
}

// Value owns immutable data. Its zero value is invalid; use Null for explicit null.
// Constructors retain invalid scalar inputs so validation can report their paths.
type Value struct {
	kind Kind
	data any
}

func Null() Value                    { return Value{kind: NullKind} }
func Bool(v bool) Value              { return Value{BoolKind, v} }
func Int(v int64) Value              { return Value{IntKind, v} }
func Float(v float64) Value          { return Value{FloatKind, v} }
func String(v string) Value          { return Value{StringKind, v} }
func Bytes(v []byte) Value           { return Value{BytesKind, append([]byte(nil), v...)} }
func Timestamp(v time.Time) Value    { return Value{TimestampKind, v.Round(0).UTC()} }
func Duration(v time.Duration) Value { return Value{DurationKind, v} }
func List(v ...Value) Value          { return Value{ListKind, append([]Value(nil), v...)} }

// OwnedList and OwnedObject accept freshly allocated compiler/runtime storage.
// Callers relinquish ownership and must never mutate it after construction.
func OwnedList(v []Value) Value            { return Value{ListKind, v} }
func OwnedObject(v map[string]Value) Value { return Value{ObjectKind, v} }

// ListView and ObjectView are read-only internal views. They must not escape
// through a public API or be mutated by a compiler/backend implementation.
func (v Value) BytesView() ([]byte, bool) {
	x, ok := v.data.([]byte)
	return x, ok && v.kind == BytesKind
}
func (v Value) ListView() ([]Value, bool) {
	x, ok := v.data.([]Value)
	return x, ok && v.kind == ListKind
}
func (v Value) ObjectView() (map[string]Value, bool) {
	x, ok := v.data.(map[string]Value)
	return x, ok && v.kind == ObjectKind
}
func Object(v map[string]Value) Value {
	copy := make(map[string]Value, len(v))
	for k, x := range v {
		copy[k] = x
	}
	return Value{ObjectKind, copy}
}
func (v Value) Kind() Kind           { return v.kind }
func (v Value) AsBool() (bool, bool) { x, ok := v.data.(bool); return x, ok && v.kind == BoolKind }
func (v Value) AsInt() (int64, bool) { x, ok := v.data.(int64); return x, ok && v.kind == IntKind }
func (v Value) AsFloat() (float64, bool) {
	x, ok := v.data.(float64)
	return x, ok && v.kind == FloatKind
}
func (v Value) AsString() (string, bool) {
	x, ok := v.data.(string)
	return x, ok && v.kind == StringKind
}
func (v Value) AsTimestamp() (time.Time, bool) {
	x, ok := v.data.(time.Time)
	return x, ok && v.kind == TimestampKind
}
func (v Value) AsDuration() (time.Duration, bool) {
	x, ok := v.data.(time.Duration)
	return x, ok && v.kind == DurationKind
}
func (v Value) AsBytes() ([]byte, bool) {
	x, ok := v.data.([]byte)
	if !ok || v.kind != BytesKind {
		return nil, false
	}
	return append([]byte(nil), x...), true
}
func (v Value) AsList() ([]Value, bool) {
	x, ok := v.data.([]Value)
	if !ok || v.kind != ListKind {
		return nil, false
	}
	return append([]Value(nil), x...), true
}
func (v Value) AsObject() (map[string]Value, bool) {
	x, ok := v.data.(map[string]Value)
	if !ok || v.kind != ObjectKind {
		return nil, false
	}
	out := make(map[string]Value, len(x))
	for k, value := range x {
		out[k] = value
	}
	return out, true
}
func (v Value) Lookup(key string) (Value, bool) {
	x, ok := v.data.(map[string]Value)
	if !ok || v.kind != ObjectKind {
		return Value{}, false
	}
	value, exists := x[key]
	return value, exists
}
