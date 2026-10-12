package sql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
)

// Enumerating rejections keeps backend limitations visible without accepting
// new or regressed cases merely because Build returned an error.
func TestSharedConformanceJSONBuildContracts(t *testing.T) {
	supported := map[string]bool{}
	for _, name := range []string{"empty_filter", "and_before_or", "grouped_or", "implicit_truthiness", "fractional_integer_division", "overflow_is_null", "unicode_code_points", "invalid_host_input_is_rejected", "typed_composite_null_identity", "typed_composite_parent_guard", "typed_array_negative_index"} {
		supported[name] = true
	}
	supported["typed_array_structural_equality"] = true
	supported["typed_array_null_membership"] = true
	supported["nonnull_guards_preserve_zero"] = true
	supported["nonnull_null_test_is_false"] = true
	supported["nullable_guards_keep_presence"] = true
	supported["typed_array_identical_guard"] = true
	supported["explicit_null_test"] = true
	supported["type_guard_failure_is_not_null"] = true
	supported["exists_observes_failed_type_guard"] = true
	supported["guard_failure_is_not_null"] = true
	supported["not_inverts_failed_guard"] = true
	supported["exists_observes_falsy_values"] = true
	supported["explicit_type_guard"] = true
	supported["any_and_all_empty_lists"] = true
	supported["callback_predicate_guard"] = true
	supported["null_type_is_observable"] = true
	supported["object_lookup_distinguishes_missing"] = true
	supported["type_guard_limits_method_permissions"] = true
	supported["not_encloses_comparison"] = true
	supported["null_inequality_is_false"] = true
	supported["negative_index"] = true
	supported["nested_callback_captures"] = true
	supported["object_dynamic_key_lookup"] = true
	supported["two_null_values_are_not_equal"] = true
	supported["not_of_null_comparison"] = true
	supported["exact_mixed_number_comparison"] = true
	supported["structural_equality_ignores_key_order"] = true
	rejected := map[string]bool{}
	for _, name := range []string{
		"json_preserves_float", "json_duplicate_keys_fail", "json_null_is_observable", "json_guard_stays_absent",
		"contains_requires_all_arguments",
		"unique_is_a_predicate",
		"fold_argument_order", "regex_partial_match", "invalid_regex_does_not_match_negation",
		"strict_base64", "duration_compound_fraction", "timestamp_utc_components", "epoch_is_truthy",
		"zero_duration_is_falsy",
		"map_preserves_null_and_order", "flatmap_preserves_order", "sort_mixed_numbers_and_reject_null", "concat_preserves_order",
		"object_enumeration_key_order", "decisive_or_skips_budgeted_callback",
		"decisive_and_skips_budgeted_callback",
	} {
		rejected[name] = true
	}
	for _, tc := range conformance.Cases() {
		t.Run(tc.Name, func(t *testing.T) {
			var bindings []hffsql.Binding
			for _, field := range tc.Schema.Fields() {
				bindings = append(bindings, hffsql.BindJSON(hff.Path{field.Name}, hffsql.Column("records", field.Name), hffsql.JSON))
			}
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities), engine.WithLimits(tc.Limits))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(context.Background(), tc.Source)
			if tc.CompileError != "" {
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Code != tc.CompileError {
					t.Fatalf("compile error=%v, want %s", err, tc.CompileError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if supported[tc.Name] {
				delete(supported, tc.Name)
				if err != nil || q.Where() == "" {
					t.Fatalf("supported case failed: %v", err)
				}
			} else if rejected[tc.Name] {
				delete(rejected, tc.Name)
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Code != hff.BackendError {
					t.Fatalf("expected explicit backend rejection, got %v", err)
				}
			} else {
				t.Fatal("new shared case needs explicit SQL coverage or rejection classification")
			}
		})
	}
	if len(supported) > 0 || len(rejected) > 0 {
		t.Fatalf("classified shared cases disappeared: supported=%v rejected=%v", supported, rejected)
	}
}
