package govalue

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	convert "github.com/ornew/hff/internal/govalue"
)

// Binding derives an immutable schema and conversion plan from T.
// Source values must not be mutated during conversion or evaluation.
type Binding[T any] struct {
	schema    hff.Type
	plans     convert.Plans
	limits    hff.Limits
	unlimited bool
}

// NewBinding derives the static Go type, rejecting recursive and unsupported
// types. Converter limit options are accepted; explicit schema/mapping options
// belong to New and cannot override an inferred contract.
func NewBinding[T any](options ...Option) (*Binding[T], error) {
	var config Config
	for _, option := range options {
		if option.apply == nil {
			return nil, configuration("invalid binding option")
		}
		if err := option.apply(&config); err != nil {
			return nil, err
		}
	}
	if config.Schema.Kind() != hff.InvalidType || len(config.Structs) != 0 {
		return nil, configuration("inferred binding does not accept explicit schema or struct mappings")
	}
	limits, err := config.Limits.Resolve()
	if err != nil {
		return nil, configuration(err.Error())
	}
	if config.UnlimitedLimits {
		limits = hff.Limits{}
	}
	i := inference{active: make(map[reflect.Type]bool), plans: make(convert.Plans)}
	typ, err := i.typ(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	if err := typ.Check(); err != nil {
		return nil, configuration(err.Error())
	}
	return &Binding[T]{schema: typ, plans: i.plans, limits: limits, unlimited: config.UnlimitedLimits}, nil
}

func (b *Binding[T]) Schema() hff.Type {
	if b == nil {
		return hff.Type{}
	}
	return b.schema
}

func (b *Binding[T]) Convert(ctx context.Context, source T) (hff.Value, error) {
	if b == nil || b.schema.Kind() == hff.InvalidType {
		return hff.Value{}, configuration("invalid binding")
	}
	value, err := convert.ConvertWithPlans(ctx, source, b.plans, b.limits)
	if err != nil {
		return hff.Value{}, err
	}
	input, err := hff.ValidateInput(ctx, b.schema, value, hff.ValidationConfig{Limits: b.limits, Unlimited: b.unlimited})
	if err != nil {
		return hff.Value{}, err
	}
	return input.Value(), nil
}

// Build constructs a typed local filter only for the exact inferred schema.
func (b *Binding[T]) Build(ctx context.Context, program hff.Program, options ...local.Option) (*Filter[T], error) {
	if b == nil || !program.Schema().Equal(b.schema) {
		return nil, configuration("program schema differs from Go binding")
	}
	filter, err := local.Build(ctx, program, options...)
	if err != nil {
		return nil, err
	}
	return &Filter[T]{binding: b, filter: filter}, nil
}

type Filter[T any] struct {
	binding *Binding[T]
	filter  *local.Filter
}

func (f *Filter[T]) Match(ctx context.Context, record T) (bool, error) {
	if f == nil {
		return false, configuration("invalid typed filter")
	}
	value, err := f.binding.Convert(ctx, record)
	if err != nil {
		return false, err
	}
	return f.filter.Match(ctx, value)
}

type inference struct {
	active map[reflect.Type]bool
	plans  convert.Plans
}

func nullable(t hff.Type) hff.Type { return hff.NewUnionType(t, hff.PrimitiveType(hff.NullType)) }
func (i *inference) typ(t reflect.Type) (hff.Type, error) {
	if i.active[t] {
		return hff.Type{}, configuration("recursive Go type: " + t.String())
	}
	i.active[t] = true
	defer delete(i.active, t)
	if t == reflect.TypeFor[time.Time]() {
		return hff.PrimitiveType(hff.TimestampType), nil
	}
	if t == reflect.TypeFor[time.Duration]() {
		return hff.PrimitiveType(hff.DurationType), nil
	}
	if t == reflect.TypeFor[hff.Value]() {
		return hff.PrimitiveType(hff.AnyType), nil
	}
	var k hff.TypeKind
	switch t.Kind() {
	case reflect.Pointer:
		inner, err := i.typ(t.Elem())
		if err != nil {
			return hff.Type{}, err
		}
		return nullable(inner), nil
	case reflect.Interface:
		if t.NumMethod() != 0 {
			return hff.Type{}, configuration("unsupported Go interface: " + t.String())
		}
		return hff.PrimitiveType(hff.AnyType), nil
	case reflect.Bool:
		k = hff.BoolType
	case reflect.String:
		k = hff.StringType
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		k = hff.IntType
	case reflect.Float32, reflect.Float64:
		k = hff.FloatType
	case reflect.Array, reflect.Slice:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return nullable(hff.PrimitiveType(hff.BytesType)), nil
		}
		e, err := i.typ(t.Elem())
		if err != nil {
			return hff.Type{}, err
		}
		out := hff.NewListType(e)
		if t.Kind() == reflect.Slice {
			out = nullable(out)
		}
		return out, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return hff.Type{}, configuration("Go map requires string keys")
		}
		e, err := i.typ(t.Elem())
		if err != nil {
			return hff.Type{}, err
		}
		return nullable(hff.NewMapType(e)), nil
	case reflect.Struct:
		var fields []hff.Field
		plan := make(map[string]convert.Field)
		if err := i.fields(t, nil, false, &fields, plan); err != nil {
			return hff.Type{}, err
		}
		i.plans[t] = plan
		return hff.NewStructType(fields...), nil
	default:
		return hff.Type{}, configuration("unsupported Go type: " + t.String())
	}
	return hff.PrimitiveType(k), nil
}
func (i *inference) fields(t reflect.Type, prefix []int, optional bool, fields *[]hff.Field, plan map[string]convert.Field) error {
	for n := 0; n < t.NumField(); n++ {
		f := t.Field(n)
		tag, ok := f.Tag.Lookup("hff")
		if !ok {
			tag = f.Tag.Get("json")
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		omit := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				omit = true
			} else if p != "" {
				return configuration("unsupported Go field tag option: " + p)
			}
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		index := append(append([]int(nil), prefix...), n)
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			if i.active[ft] {
				return configuration("recursive embedded Go type: " + ft.String())
			}
			i.active[ft] = true
			err := i.fields(ft, index, optional || f.Type.Kind() == reflect.Pointer, fields, plan)
			delete(i.active, ft)
			if err != nil {
				return err
			}
			continue
		}
		if f.PkgPath != "" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if _, exists := plan[name]; exists {
			return configuration("ambiguous Go field name: " + name)
		}
		typ, err := i.typ(f.Type)
		if err != nil {
			return err
		}
		*fields = append(*fields, hff.Field{Name: name, Type: typ, Optional: optional || omit})
		plan[name] = convert.Field{Index: index, OmitEmpty: omit}
	}
	return nil
}
