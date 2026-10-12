// Package govalue contains the reflection conversion shared by Go adapters.
package govalue

import (
	"context"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

// Fields explicitly maps HFF names to direct exported fields of a Go struct.
// There is no implicit tag, case conversion, or embedded-field promotion.
type Fields map[reflect.Type]map[string]string

// Field describes a promoted Go field and its JSON-style omission policy.
type Field struct {
	Index     []int
	OmitEmpty bool
}

// Plans contains immutable field plans compiled by a type-derived binding.
type Plans map[reflect.Type]map[string]Field

type identity struct {
	typ    reflect.Type
	ptr    uintptr
	length int
}

type conversion struct {
	ctx    context.Context
	fields Fields
	plans  Plans
	limits hff.Limits
	count  int
	active map[identity]bool
}

// Convert creates an owned value, preserving exact supported Go representations.
// Limits bound the traversal before allocation, independently of later validation.
// The caller resolves defaults first; zero traversal ceilings disable that bound.
// Existing immutable HFF values pass through for validation by the caller.
// The caller must not mutate the source or mappings during conversion.
func Convert(ctx context.Context, source any, fields Fields, limits hff.Limits) (hff.Value, error) {
	return convertSource(ctx, source, fields, nil, limits)
}

// ConvertWithPlans uses compiled promoted fields instead of explicit mappings.
func ConvertWithPlans(ctx context.Context, source any, plans Plans, limits hff.Limits) (hff.Value, error) {
	return convertSource(ctx, source, nil, plans, limits)
}
func convertSource(ctx context.Context, source any, fields Fields, plans Plans, limits hff.Limits) (hff.Value, error) {
	if ctx == nil {
		return hff.Value{}, failure(hff.ConfigurationError, nil, "nil context")
	}
	if limits.MaxInputDepth < 0 || limits.MaxInputValues < 0 {
		return hff.Value{}, failure(hff.ConfigurationError, nil, "negative Go input limit")
	}
	c := conversion{ctx: ctx, fields: fields, plans: plans, limits: limits, active: make(map[identity]bool)}
	v, err := c.value(reflect.ValueOf(source), nil, 0)
	if err != nil {
		return hff.Value{}, err
	}
	if err := core.CheckContext(ctx); err != nil {
		return hff.Value{}, err
	}
	return v, nil
}

func failure(code hff.ErrorCode, path hff.Path, message string) error {
	return &hff.Error{Code: code, Path: append(hff.Path(nil), path...), Message: message}
}

func child(path hff.Path, name string) hff.Path {
	out := make(hff.Path, len(path)+1)
	copy(out, path)
	out[len(path)] = name
	return out
}

func (c *conversion) value(v reflect.Value, path hff.Path, depth int) (hff.Value, error) {
	if err := c.visit(path, depth); err != nil {
		return hff.Value{}, err
	}
	// Unwrap pointer/interface chains iteratively. Keep their identities active
	// until the complete subtree has been converted, so aliases are allowed but
	// ancestor cycles are rejected without recursing through pointer chains.
	var entered []identity
	defer func() {
		for _, id := range entered {
			delete(c.active, id)
		}
	}()
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if err := core.CheckContext(c.ctx); err != nil {
			return hff.Value{}, err
		}
		if v.IsNil() {
			return hff.Null(), nil
		}
		if v.Kind() == reflect.Pointer {
			id := identity{typ: v.Type(), ptr: v.Pointer()}
			if c.active[id] {
				return hff.Value{}, failure(hff.InputError, path, "cyclic Go input")
			}
			c.active[id] = true
			entered = append(entered, id)
		}
		depth++
		if err := c.visit(path, depth); err != nil {
			return hff.Value{}, err
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return hff.Null(), nil
	}
	if v.CanInterface() {
		switch x := v.Interface().(type) {
		case hff.Value:
			if x.Kind() == hff.InvalidKind {
				return hff.Value{}, failure(hff.InputError, path, "invalid HFF value")
			}
			return x, nil
		case time.Time:
			return hff.Timestamp(x), nil
		case time.Duration:
			return hff.Duration(x), nil
		}
	}
	if v.Kind() == reflect.Map || v.Kind() == reflect.Slice {
		if v.IsNil() {
			return hff.Null(), nil
		}
		id := identity{typ: v.Type(), ptr: uintptr(v.UnsafePointer())}
		if v.Kind() == reflect.Slice {
			id.length = v.Len()
		}
		if c.active[id] {
			return hff.Value{}, failure(hff.InputError, path, "cyclic Go input")
		}
		c.active[id] = true
		entered = append(entered, id)
	}
	switch v.Kind() {
	case reflect.Bool:
		return hff.Bool(v.Bool()), nil
	case reflect.String:
		return hff.String(v.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return hff.Int(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() > math.MaxInt64 {
			return hff.Value{}, failure(hff.InputError, path, "unsigned Go integer exceeds int64")
		}
		return hff.Int(int64(v.Uint())), nil
	case reflect.Float32, reflect.Float64:
		x := v.Float()
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return hff.Value{}, failure(hff.InputError, path, "non-finite Go number")
		}
		return hff.Float(x), nil
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return hff.Bytes(v.Bytes()), nil
		}
		fallthrough
	case reflect.Array:
		if err := c.reserve(v.Len(), path); err != nil {
			return hff.Value{}, err
		}
		out := make([]hff.Value, v.Len())
		for i := range out {
			x, err := c.value(v.Index(i), child(path, strconv.Itoa(i)), depth+1)
			if err != nil {
				return hff.Value{}, err
			}
			out[i] = x
		}
		return hff.List(out...), nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return hff.Value{}, failure(hff.InputError, path, "Go map requires string keys")
		}
		if err := c.reserve(v.Len(), path); err != nil {
			return hff.Value{}, err
		}
		out := make(map[string]hff.Value, v.Len())
		entries := v.MapRange()
		for entries.Next() {
			key := entries.Key().String()
			x, err := c.value(entries.Value(), child(path, key), depth+1)
			if err != nil {
				return hff.Value{}, err
			}
			out[key] = x
		}
		return hff.Object(out), nil
	case reflect.Struct:
		if plan, ok := c.plans[v.Type()]; ok {
			if err := c.reserve(len(plan), path); err != nil {
				return hff.Value{}, err
			}
			out := make(map[string]hff.Value, len(plan))
			for name, field := range plan {
				fv := v
				missing := false
				fieldDepth := depth
				for offset, index := range field.Index {
					if fv.Kind() == reflect.Pointer {
						if fv.IsNil() {
							missing = true
							break
						}
						fv = fv.Elem()
						fieldDepth++
						if err := c.visit(child(path, name), fieldDepth); err != nil {
							return hff.Value{}, err
						}
					}
					fv = fv.Field(index)
					fieldDepth++
					if offset < len(field.Index)-1 {
						if err := c.visit(child(path, name), fieldDepth); err != nil {
							return hff.Value{}, err
						}
					}
				}
				if missing || (field.OmitEmpty && empty(fv)) {
					continue
				}
				x, err := c.value(fv, child(path, name), fieldDepth)
				if err != nil {
					return hff.Value{}, err
				}
				out[name] = x
			}
			return hff.Object(out), nil
		}
		fields, ok := c.fields[v.Type()]
		if !ok {
			return hff.Value{}, failure(hff.InputError, path, "Go struct requires an explicit field mapping")
		}
		if err := c.reserve(len(fields), path); err != nil {
			return hff.Value{}, err
		}
		out := make(map[string]hff.Value, len(fields))
		for name, goName := range fields {
			field, ok := v.Type().FieldByName(goName)
			if !ok || field.PkgPath != "" || len(field.Index) != 1 {
				return hff.Value{}, failure(hff.ConfigurationError, child(path, name), "mapping requires a direct exported Go field")
			}
			x, err := c.value(v.Field(field.Index[0]), child(path, name), depth+1)
			if err != nil {
				return hff.Value{}, err
			}
			out[name] = x
		}
		return hff.Object(out), nil
	default:
		return hff.Value{}, failure(hff.InputError, path, "unsupported Go value type")
	}
}

func (c *conversion) visit(path hff.Path, depth int) error {
	if err := core.CheckContext(c.ctx); err != nil {
		return err
	}
	if c.limits.MaxInputDepth > 0 && depth > c.limits.MaxInputDepth {
		return failure(hff.LimitError, path, "Go input depth limit exceeded")
	}
	c.count++
	if c.limits.MaxInputValues > 0 && c.count > c.limits.MaxInputValues {
		return failure(hff.LimitError, path, "Go input value limit exceeded")
	}
	return nil
}

func (c *conversion) reserve(n int, path hff.Path) error {
	if c.limits.MaxInputValues > 0 && n > c.limits.MaxInputValues-c.count {
		return failure(hff.LimitError, path, "Go input value limit exceeded")
	}
	return nil
}

func empty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}
