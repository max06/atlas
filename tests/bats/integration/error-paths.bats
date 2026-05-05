#!/usr/bin/env bats
#
# Scenario: ATLAS template error paths produce helpful fail messages.
#
# Each case uses a dedicated fixture under tests/fixtures-negative/ and
# targets a single deployment via ATLAS_FILTER_* to avoid cascading errors
# from other negative fixtures in the same directory.

_root() { cd "${BATS_TEST_DIRNAME}/../../.." && pwd; }

# _render_negative runs helmfile template against fixtures-negative, filtered
# to a single deployment. Returns non-zero on template failure (expected).
_render_negative() {
  local deployment="$1" root
  root="$(_root)"
  ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME="$deployment" \
    helmfile -f "$root/helmfile.yaml.gotmpl" \
      --state-values-set "atlas.appTemplates=tests/templates" \
      --state-values-set "atlas.deploymentDefinitions=tests/fixtures-negative" \
      --state-values-set "atlas.cwd=$root" \
      template --skip-schema-validation 2>&1
}

# --- invalid nameStyle --------------------------------------------------------

@test "invalid nameStyle: render fails" {
  run _render_negative invalid-namestyle
  [ "$status" -ne 0 ]
}

@test "invalid nameStyle: error mentions the bad value" {
  run _render_negative invalid-namestyle
  echo "$output" | grep -q "diagonal"
}

@test "invalid nameStyle: error mentions prefix/suffix" {
  run _render_negative invalid-namestyle
  echo "$output" | grep -q "prefix.*suffix\|suffix.*prefix"
}

# --- missing app template -----------------------------------------------------

@test "missing template: render fails" {
  run _render_negative missing-template
  [ "$status" -ne 0 ]
}

@test "missing template: error names the template" {
  run _render_negative missing-template
  echo "$output" | grep -q "app-does-not-exist"
}

@test "missing template: error says 'not found'" {
  run _render_negative missing-template
  echo "$output" | grep -qi "not found"
}
