package mcpsrv

import (
	"os/exec"
	"strings"
	"testing"
)

// portedTests names every test in mcp/tests and the Go test that replaced
// it. Two Python tests have no counterpart: they assert that a non-string
// argument is refused, which Go's type system makes unreachable.
var portedTests = map[string]string{
	"test_vm_name_accepts_ordinary_names":                                     "TestCheckVMName",
	"test_vm_name_rejects_bad_input":                                          "TestCheckVMName",
	"test_vm_name_rejects_non_string":                                         "",
	"test_vm_name_rejects_absolute_path":                                      "TestCheckVMName",
	"test_vm_name_rejects_unicode_lookalike_separator":                        "TestCheckVMName",
	"test_image_id_accepts_catalog_ids":                                       "TestCheckImageID",
	"test_image_id_rejects_paths":                                             "TestCheckImageID",
	"test_image_id_rejects_non_string":                                        "",
	"test_host_path_accepts_file_inside_sandbox":                              "TestCheckHostPath",
	"test_host_path_accepts_the_sandbox_root_itself":                          "TestCheckHostPath",
	"test_host_path_rejects_relative_path":                                    "TestCheckHostPath",
	"test_host_path_rejects_traversal_out_of_sandbox":                         "TestCheckHostPath",
	"test_host_path_rejects_absolute_path_entirely_outside":                   "TestCheckHostPath",
	"test_host_path_rejects_sibling_sharing_a_string_prefix":                  "TestCheckHostPath",
	"test_host_path_rejects_symlink_escape":                                   "TestCheckHostPath",
	"test_host_path_rejects_symlink_pointing_directly_out":                    "TestCheckHostPath",
	"test_host_path_accepts_symlink_that_stays_inside_sandbox":                "TestCheckHostPath",
	"test_host_path_rejects_empty_and_whitespace":                             "TestCheckHostPath",
	"test_host_path_rejects_null_byte":                                        "TestCheckHostPath",
	"test_host_path_expands_tilde_to_the_callers_home_not_ours":               "TestCheckHostPath",
	"test_host_path_different_vm_names_have_disjoint_sandboxes":               "TestCheckHostPath",
	"test_host_path_rejects_invalid_vm_name_even_as_a_path_component":         "TestCheckHostPath",
	"test_strip_forbidden_removes_every_forbidden_key":                        "TestStripForbidden",
	"test_strip_forbidden_is_a_no_op_on_a_clean_patch":                        "TestStripForbidden",
	"test_strip_forbidden_does_not_mutate_the_input":                          "TestStripForbidden",
	"test_check_flag_free_rejects_a_kong_flag":                                "TestCheckFlagFree",
	"test_check_flag_free_rejects_a_short_flag_and_empty_values":              "TestCheckFlagFree",
	"test_check_flag_free_passes_ordinary_values":                             "TestCheckFlagFree",
	"test_rate_limiter_allows_up_to_capacity":                                 "TestRateLimiter",
	"test_rate_limiter_buckets_are_independent_per_tool":                      "TestRateLimiter",
	"test_rate_limiter_refills_over_time":                                     "TestRateLimiter",
	"test_rate_limiter_shared_bucket_bounds_every_tool_together":              "TestRateLimiter",
	"test_rate_limiter_refused_tool_does_not_spend_a_shared_token":            "TestRateLimiter",
	"test_every_expected_tool_is_registered":                                  "TestEveryTableToolIsRegistered",
	"test_no_unexpected_tools_are_registered":                                 "TestNoToolOutsideTheTable",
	"test_forbidden_surfaces_are_not_registered_at_all":                       "TestForbiddenSurfacesAbsent",
	"test_tool_schema_sets_additional_properties_false":                       "TestInputSchemaRejectsAdditionalProperties",
	"test_annotations_match_the_spec_table":                                   "TestAnnotationsMatchTable",
	"test_no_tool_has_a_parameter_named_share":                                "TestNoForbiddenInputField",
	"test_no_tool_has_a_parameter_named_console_password_or_image_path_flags": "TestNoForbiddenInputField",
	"test_create_only_accepts_a_catalog_image_id_field_named_image":           "TestCreateTakesCatalogImageIDOnly",
	"test_every_tool_has_a_non_empty_human_readable_description":              "TestEveryToolHasDescription",
	"test_no_tool_description_contains_an_em_dash":                            "TestNoEmDashInDescription",
	"test_apply_recipes_refuses_a_vm_with_allow_exec_false":                   "TestRequireAccess",
	"test_plan_recipes_runs_a_dry_run_and_needs_no_exec_permission":           "TestPlanRecipesNeedsNoAccess",
	"test_wait_clamps_the_timeout":                                            "TestWaitClampsTimeout",
	"test_logs_clamps_the_line_count":                                         "TestLogsClampsLines",
	"test_forward_refuses_a_pair_that_kong_reads_as_a_flag":                   "TestForwardRefusesFlagPair",
}

// TestEveryPortedTestExists asserts each named Go test is in this package's
// test binary. It is what makes deleting mcp/ safe: a row naming a test that
// does not exist fails here rather than being discovered after the Python is
// gone.
func TestEveryPortedTestExists(t *testing.T) {
	out, err := exec.Command("go", "test", "-list", ".*", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go test -list: %v\n%s", err, out)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	for py, goTest := range portedTests {
		if goTest == "" {
			continue
		}
		if !have[goTest] {
			t.Errorf("%s maps to %s, which this package does not define", py, goTest)
		}
	}
}
