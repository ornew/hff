package sql

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func uniformGuardRoles() *jsonItemRoles {
	return &jsonItemRoles{object: hff.NewStructType(
		hff.Field{Name: "a", Type: hff.PrimitiveType(hff.BytesType)},
		hff.Field{Name: "z", Type: hff.PrimitiveType(hff.BytesType)},
	)}
}

func TestUniformItemMatcherKeepsOnlyFallbackDependencies(t *testing.T) {
	b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7, jsonValues: map[string]JSONFormat{"existing": JSON}}
	var fallback string
	calls := 0
	got, err := b.jsonItemUniformValueOperation(hff.Node{}, "pair", uniformGuardRoles(), func(probe *builder, source hff.Type) (string, error) {
		calls++
		parameter, err := probe.parameter(hff.Node{}, hff.String("branch"))
		if err != nil {
			return "", err
		}
		projected := probe.project(parameter.code)
		probe.jsonValues[projected] = JSON
		if calls == 1 {
			if source.Kind() != hff.NeverType {
				t.Fatal("first callback was not fallback", source.Key())
			}
			fallback = projected + " IS NOT NULL"
		}
		return fallback, nil
	})
	if err != nil || got != fallback || calls != 3 {
		t.Fatal(got, calls, err)
	}
	if !reflect.DeepEqual(b.args, []any{"existing", "branch"}) || b.alias != 8 || len(b.steps) != 1 || len(b.jsonValues) != 2 {
		t.Fatal("discarded branch state survived", b.args, b.alias, b.steps, b.jsonValues)
	}
	// A later sibling can reuse discarded parameter/alias numbers without
	// changing the fallback expression's dependencies.
	parameter, err := b.parameter(hff.Node{}, hff.String("sibling"))
	if err != nil || parameter.code != "($3::text)" {
		t.Fatal(parameter, err)
	}
	projected := b.project(parameter.code)
	if projected != `"__hff_9".v` || !strings.Contains(b.steps[1], "($3::text)") {
		t.Fatal(projected, b.steps)
	}
}

func TestNonuniformItemMatcherKeepsCompleteOriginalState(t *testing.T) {
	makeMatcher := func(probe *builder) func(hff.Type) (string, error) {
		return func(hff.Type) (string, error) {
			parameter, err := probe.parameter(hff.Node{}, hff.String("branch"))
			if err != nil {
				return "", err
			}
			return probe.project(parameter.code) + " IS NOT NULL", nil
		}
	}
	ordinary := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
	want, err := ordinary.jsonItemValueOperation(hff.Node{}, "pair", uniformGuardRoles(), makeMatcher(ordinary))
	if err != nil {
		t.Fatal(err)
	}
	b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
	var match func(hff.Type) (string, error)
	got, err := b.jsonItemUniformValueOperation(hff.Node{}, "pair", uniformGuardRoles(), func(probe *builder, source hff.Type) (string, error) {
		if match == nil {
			match = makeMatcher(probe)
		}
		return match(source)
	})
	if err != nil || got != want || b.alias != ordinary.alias || !reflect.DeepEqual(b.args, ordinary.args) || !reflect.DeepEqual(b.steps, ordinary.steps) {
		t.Fatal(got, want, b.args, ordinary.args, err)
	}
}

func TestUniformItemMatcherValidatesLaterBranchesAndKeys(t *testing.T) {
	failure := errors.New("late matcher failure")
	for _, mode := range []string{"error", "cancel", "last cancel", "NUL", "UTF8"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := &builder{ctx: ctx, args: []any{"existing"}, alias: 7}
			roles := uniformGuardRoles()
			if mode == "NUL" || mode == "UTF8" {
				key := "z\x00bad"
				if mode == "UTF8" {
					key = "z\xffbad"
				}
				roles = &jsonItemRoles{object: hff.NewStructType(hff.Field{Name: "a", Type: hff.PrimitiveType(hff.BytesType)}, hff.Field{Name: key, Type: hff.PrimitiveType(hff.BytesType)})}
			}
			calls := 0
			_, err := b.jsonItemUniformValueOperation(hff.Node{}, "pair", roles, func(probe *builder, _ hff.Type) (string, error) {
				calls++
				if calls == 3 && mode == "error" {
					return "", failure
				}
				if (calls == 2 && mode == "cancel") || (calls == 3 && mode == "last cancel") {
					cancel()
				}
				_, err := probe.parameter(hff.Node{}, hff.String("temporary"))
				return "TRUE", err
			})
			if err == nil {
				t.Fatal("late validation ignored", mode)
			}
			if mode == "error" && !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if strings.Contains(mode, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(b.args, []any{"existing"}) || b.alias != 7 || len(b.steps) != 0 {
				t.Fatal("failed probe leaked state", b.args, b.alias)
			}
		})
	}
}

func TestUniformNullableItemMatcherOmitsKeyDispatch(t *testing.T) {
	bytes := hff.NewUnionType(hff.PrimitiveType(hff.BytesType), hff.PrimitiveType(hff.NullType))
	target := hff.NewUnionType(hff.PrimitiveType(hff.StringType), hff.PrimitiveType(hff.NullType))
	for _, format := range []JSONFormat{JSON, JSONB} {
		roles := &jsonItemRoles{object: hff.NewStructType(hff.Field{Name: "a", Type: bytes}, hff.Field{Name: "z", Type: bytes})}
		b := &builder{ctx: context.Background()}
		got, err := b.jsonItemValueMatches(hff.Node{}, "pair", format, roles, target)
		ordinary := &builder{ctx: context.Background()}
		want, wantErr := ordinary.jsonMatches(hff.Node{}, "(pair -> 1)", format, hff.PrimitiveType(hff.NeverType), target)
		if err != nil || wantErr != nil || got != want || len(b.args) != 0 {
			t.Fatal(format, got, want, b.args, err, wantErr)
		}
		if got == "TRUE" || got == "FALSE" {
			t.Fatal("nonconstant predicate replaced with constant", got)
		}
	}
}

func TestUniformSlotOperationRetainsKeyDependenciesAndConstraints(t *testing.T) {
	b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
	slot := jsonItemSlot{raw: "raw", pair: "pair", key: "is_key", value: "is_value", format: JSON, roles: uniformGuardRoles(), constraints: []hff.Type{hff.PrimitiveType(hff.NullType)}}
	var keySQL, valueSQL string
	calls := 0
	got, err := b.jsonItemSlotUniformOperation(hff.Node{}, slot, "FALSE", func(probe *builder, source hff.Type) (string, error) {
		calls++
		if source.Kind() != hff.NeverType {
			t.Fatal("prior slot constraint ignored", source.Key())
		}
		parameter, err := probe.parameter(hff.Node{}, hff.String("dependency"))
		if err != nil {
			return "", err
		}
		code := probe.project(parameter.code) + " IS NOT NULL"
		if calls == 1 {
			keySQL = code
			return code, nil
		}
		if calls == 2 {
			valueSQL = code
		}
		return valueSQL, nil
	})
	want := "(CASE WHEN is_key THEN " + keySQL + " WHEN is_value THEN " + valueSQL + " ELSE FALSE END)"
	if err != nil || got != want || calls != 4 || len(b.args) != 3 || len(b.steps) != 2 || b.alias != 9 {
		t.Fatal(got, want, calls, b.args, b.steps, b.alias, err)
	}
}

func TestNonuniformSlotNormalizationKeepsOriginalSQLAndState(t *testing.T) {
	text, floating := hff.PrimitiveType(hff.StringType), hff.PrimitiveType(hff.FloatType)
	target := hff.NewUnionType(floating, hff.PrimitiveType(hff.NullType))
	roles := &jsonItemRoles{object: hff.NewStructType(hff.Field{Name: "a", Type: text}, hff.Field{Name: "z", Type: floating})}
	slot := jsonItemSlot{raw: "raw", pair: "pair", key: "FALSE", value: "TRUE", format: JSON, roles: roles}
	ordinary := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
	want, err := ordinary.jsonItemSlotOperation(hff.Node{}, slot, "'null'::json", func(source hff.Type) (string, error) {
		return ordinary.normalizedComparisonDocument(hff.Node{}, slot.raw, JSON, intersectItemType(source, target))
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
	got, err := b.jsonItemSlotUniformOperation(hff.Node{}, slot, "'null'::json", func(probe *builder, source hff.Type) (string, error) {
		return probe.normalizedComparisonDocument(hff.Node{}, slot.raw, JSON, intersectItemType(source, target))
	})
	if err != nil || got != want || b.alias != ordinary.alias || !reflect.DeepEqual(b.args, ordinary.args) || !reflect.DeepEqual(b.steps, ordinary.steps) || !reflect.DeepEqual(b.normalizedJSONValues, ordinary.normalizedJSONValues) {
		t.Fatal(got, want, b.args, ordinary.args, err)
	}
}
