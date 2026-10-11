package core

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Input is a validated immutable record, reusable with the same schema contract.
// Its zero value is invalid. ValidateInput is the only constructor.
type Input struct {
	value  Value
	schema Type
	valid  bool
}

func (i Input) Value() Value           { return i.value }
func (i Input) Schema() Type           { return i.schema }
func (i Input) Compatible(t Type) bool { return i.valid && i.schema.Equal(t) }

// ValidationConfig makes disabling limits an explicit host choice.
type ValidationConfig struct {
	Limits    Limits
	Unlimited bool
}

// ValidateInput validates the full value, including all nested Any data, before
// checking schema conformance. Zero limits select defaults; unlimited is explicit.
func ValidateInput(ctx context.Context, schema Type, value Value, config ValidationConfig) (Input, error) {
	if ctx == nil {
		return Input{}, &Error{Code: ConfigurationError, Message: "nil context"}
	}
	if err := schema.Check(); err != nil {
		return Input{}, &Error{Code: ConfigurationError, Message: err.Error()}
	}
	limits, err := config.Limits.Resolve()
	if err != nil {
		return Input{}, &Error{Code: ConfigurationError, Message: err.Error()}
	}
	if config.Unlimited {
		limits = Limits{}
	}
	count := 0
	var scratch [65]segment
	if err := validateValue(ctx, value, scratch[:0], 0, &count, limits); err != nil {
		return Input{}, err
	}
	if err := checkValueType(ctx, value, schema, scratch[:0]); err != nil {
		return Input{}, err
	}
	return Input{value, schema, true}, nil
}
func cancelError(ctx context.Context) error {
	return CheckContext(ctx)
}

type segment struct {
	key     string
	index   int
	indexed bool
}
type location []segment

func locationError(code ErrorCode, path location, message string) error {
	names := make(Path, len(path))
	for i, part := range path {
		if part.indexed {
			names[i] = strconv.Itoa(part.index)
		} else {
			names[i] = part.key
		}
	}
	return &Error{Code: code, Path: names, Message: message}
}
func inputError(path location, message string) error { return locationError(InputError, path, message) }
func appendPath(path location, key string) location  { return append(path, segment{key: key}) }
func appendIndex(path location, index int) location {
	return append(path, segment{index: index, indexed: true})
}

func objectKeys(m map[string]Value) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func validateValue(ctx context.Context, v Value, path location, depth int, count *int, limits Limits) error {
	if err := cancelError(ctx); err != nil {
		return err
	}
	*count += 1
	if limits.MaxInputDepth > 0 && depth > limits.MaxInputDepth {
		return locationError(LimitError, path, "input depth limit exceeded")
	}
	if limits.MaxInputValues > 0 && *count > limits.MaxInputValues {
		return locationError(LimitError, path, "input value count limit exceeded")
	}
	switch v.kind {
	case InvalidKind:
		return inputError(path, "invalid Value; use Null() for explicit null")
	case StringKind:
		valid, err := inputUTF8(ctx, v.data.(string))
		if err != nil {
			return err
		}
		if !valid {
			return inputError(path, "string is not valid UTF-8")
		}
	case FloatKind:
		x := v.data.(float64)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return inputError(path, "float64 must be finite")
		}
	case TimestampKind:
		t, _ := v.AsTimestamp()
		if t.Year() < 0 || t.Year() > 9999 {
			return inputError(path, "timestamp year is outside 0000..9999")
		}
	case ListKind:
		for i, x := range v.data.([]Value) {
			if err := validateValue(ctx, x, appendIndex(path, i), depth+1, count, limits); err != nil {
				return err
			}
		}
	case ObjectKind:
		if limits.MaxInputValues > 0 && len(v.data.(map[string]Value)) > limits.MaxInputValues-*count {
			return locationError(LimitError, path, "input value count limit exceeded")
		}
		m := v.data.(map[string]Value)
		for _, key := range objectKeys(m) {
			valid, err := inputUTF8(ctx, key)
			if err != nil {
				return err
			}
			if !valid {
				return inputError(appendPath(path, key), "key is not valid UTF-8")
			}
			if err := validateValue(ctx, m[key], appendPath(path, key), depth+1, count, limits); err != nil {
				return err
			}
		}
	}
	return nil
}

// Keep individual UTF-8 scans bounded so cancellation can interrupt validation
// of a large host string or key. Split only at rune boundaries; invalid runs of
// continuation bytes remain invalid rather than being skipped or replaced.
func inputUTF8(ctx context.Context, text string) (bool, error) {
	const chunkBytes = 4096
	for len(text) > 0 {
		if err := cancelError(ctx); err != nil {
			return false, err
		}
		end := min(len(text), chunkBytes)
		if end < len(text) {
			for end > 0 && !utf8.RuneStart(text[end]) {
				end--
			}
			if end == 0 {
				return false, nil
			}
		}
		if !utf8.ValidString(text[:end]) {
			return false, nil
		}
		text = text[end:]
	}
	return true, nil
}
func admitsNull(t Type) bool {
	if t.Kind() == NullType {
		return true
	}
	if t.Kind() == UnionType {
		for _, m := range t.node.members {
			if admitsNull(m) {
				return true
			}
		}
	}
	return false
}
func checkValueType(ctx context.Context, v Value, t Type, path location) error {
	if err := cancelError(ctx); err != nil {
		return err
	}
	if t.Kind() == AnyType {
		return nil
	}
	if t.Kind() == UnionType {
		for _, member := range t.node.members {
			err := checkValueType(ctx, v, member, path)
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return cancelError(ctx)
			}
		}
		return inputError(path, "value does not match any union member")
	}
	expected := [...]Kind{InvalidKind, NullKind, BoolKind, IntKind, FloatKind, StringKind, BytesKind, ListKind, ObjectKind, ObjectKind, TimestampKind, DurationKind, InvalidKind, InvalidKind, InvalidKind}
	if int(t.Kind()) >= len(expected) || expected[t.Kind()] == InvalidKind || expected[t.Kind()] != v.Kind() {
		return inputError(path, fmt.Sprintf("unexpected %s value for schema type %s", v.Kind(), t.Key()))
	}
	switch t.Kind() {
	case ListType:
		for i, x := range v.data.([]Value) {
			if err := checkValueType(ctx, x, t.Element(), appendIndex(path, i)); err != nil {
				return err
			}
		}
	case MapType:
		m := v.data.(map[string]Value)
		for _, key := range objectKeys(m) {
			if err := checkValueType(ctx, m[key], t.Element(), appendPath(path, key)); err != nil {
				return err
			}
		}
	case StructType:
		m := v.data.(map[string]Value)
		known := make(map[string]bool, len(t.node.fields))
		for _, field := range t.node.fields {
			known[field.Name] = true
			x, present := m[field.Name]
			if !present {
				if field.Optional || admitsNull(field.Type) {
					continue
				}
				return inputError(appendPath(path, field.Name), "required field is missing")
			}
			if err := checkValueType(ctx, x, field.Type, appendPath(path, field.Name)); err != nil {
				return err
			}
		}
		for _, key := range objectKeys(m) {
			if !known[key] {
				return inputError(appendPath(path, key), "undeclared struct field; use a map schema for dynamic keys")
			}
		}
	}
	return nil
}
