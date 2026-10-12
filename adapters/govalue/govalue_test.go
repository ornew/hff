package govalue_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/adapters/govalue"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestMappedRecordRunsAnActualFilter(t *testing.T) {
	type person struct {
		Name string
		Age  int64
	}
	typ := schema.Struct(schema.String("name"), schema.Int("age"))
	config := &govalue.Config{Schema: typ, Structs: []govalue.StructMapping{{Type: reflect.TypeFor[person](), Fields: map[string]string{"name": "Name", "age": "Age"}}}}
	converter, err := govalue.New(govalue.WithConfig(config))
	if err != nil {
		t.Fatal(err)
	}
	// The converter keeps its own immutable mapping, not the caller's map.
	config.Structs[0].Fields["name"] = "Missing"
	config.Schema = schema.IntType
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `name="Alice" age>=18`)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source person
		match  bool
	}{{person{"Alice", 20}, true}, {person{"Bob", 20}, false}, {person{"Alice", 17}, false}} {
		value, err := converter.Convert(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		input, err := e.Validate(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		match, err := filter.MatchInput(ctx, input)
		if err != nil || match != tc.match {
			t.Fatal(match, err)
		}
	}
}

func TestMissingNullAndEmptyRemainDistinct(t *testing.T) {
	typ := schema.Struct(schema.Optional(schema.String("missing")), schema.Nullable(schema.List("items", schema.IntType)))
	c, err := govalue.New(govalue.WithSchema(typ))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []map[string]any{{}, {"items": []int(nil)}, {"items": []int{}}} {
		value, err := c.Convert(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := value.Lookup("missing"); exists {
			t.Fatal("missing field invented")
		}
		items, present := value.Lookup("items")
		if _, supplied := source["items"]; !supplied {
			if present {
				t.Fatal("missing list invented")
			}
		} else if reflect.ValueOf(source["items"]).IsNil() {
			if items.Kind() != hff.NullKind {
				t.Fatal("nil list was not null")
			}
		} else if values, ok := items.AsList(); !ok || len(values) != 0 {
			t.Fatal("empty list was not retained")
		}
	}
}

func TestSchemaNeverCoercesValues(t *testing.T) {
	for _, tc := range []struct {
		typ    hff.Type
		source any
	}{
		{schema.FloatType, int64(1)}, {schema.IntType, 1.0},
		{schema.BytesType, "a"}, {schema.StringType, []byte("a")},
		{schema.StringType, string([]byte{255})},
		{schema.Struct(schema.Int("age")), map[string]any{"age": 1, "extra": 2}},
	} {
		c, err := govalue.New(govalue.WithSchema(tc.typ))
		if err != nil {
			t.Fatal(err)
		}
		value, err := c.Convert(context.Background(), tc.source)
		assertError(t, value, err, hff.InputError)
	}
}

func assertError(t *testing.T, value hff.Value, err error, code hff.ErrorCode) {
	t.Helper()
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != code || value.Kind() != hff.InvalidKind {
		t.Fatalf("value=%v err=%v, expected %s without partial value", value.Kind(), err, code)
	}
}

func TestLimitsAndConfiguration(t *testing.T) {
	c, err := govalue.New(govalue.WithConfig(&govalue.Config{Schema: schema.IntType}), govalue.WithSchema(schema.ListOf(schema.IntType)), govalue.WithLimits(hff.Limits{MaxInputValues: 2}))
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Convert(context.Background(), []int{1, 2})
	assertError(t, v, err, hff.LimitError)
	c, err = govalue.New(govalue.WithSchema(schema.ListOf(schema.IntType)), govalue.WithLimits(hff.Limits{MaxInputValues: 2}), govalue.WithUnlimitedLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Convert(context.Background(), []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	for _, options := range [][]govalue.Option{nil, {{}}, {govalue.WithConfig(nil)}, {govalue.WithSchema(schema.AnyType), govalue.WithLimits(hff.Limits{MaxInputValues: -1})}, {govalue.WithSchema(schema.AnyType), govalue.WithStructs(govalue.StructMapping{Type: reflect.TypeFor[int]()})}} {
		_, err := govalue.New(options...)
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.ConfigurationError {
			t.Fatal(err)
		}
	}
	var zero govalue.Converter
	v, err = zero.Convert(context.Background(), 1)
	assertError(t, v, err, hff.ConfigurationError)
	v, err = c.Convert(nil, 1)
	assertError(t, v, err, hff.ConfigurationError)
}
