#!/usr/bin/env bats
#
# Scenario: explicit file references that do not exist fail the render
# loudly instead of being silently skipped (issues #61, #62).
#
# Same harness as error-paths.bats: dedicated fixtures under
# tests/fixtures-negative/, isolated per deployment via ATLAS_FILTER_*.

_root() { cd "${BATS_TEST_DIRNAME}/../../.." && pwd; }

_render_negative() {
  local deployment="$1" root
  root="$(_root)"
  ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME="$deployment" \
    helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
      --state-values-set "atlas.deploymentDefinitions=fixtures-negative" \
      template --skip-schema-validation 2>&1
}

# --- template-level strategicMergePatches (issue #61) -------------------------

@test "missing patch file: render fails" {
  run _render_negative missing-patch
  [ "$status" -ne 0 ]
}

@test "missing patch file: error names the field and resolved path" {
  run _render_negative missing-patch
  echo "$output" | grep -q "strategicMergePatches: file not found"
  echo "$output" | grep -q "patches/does-not-exist.yaml"
}

# --- instance-level values file (issue #62) -----------------------------------

@test "missing instance values file: render fails" {
  run _render_negative missing-values-file
  [ "$status" -ne 0 ]
}

@test "missing instance values file: error names release and path" {
  run _render_negative missing-values-file
  echo "$output" | grep -q "instance values file not found"
  echo "$output" | grep -q "no-such-values.yaml"
}

# --- instance-level secrets file (issue #62) ----------------------------------

@test "missing instance secrets file: render fails" {
  run _render_negative missing-secrets-file
  [ "$status" -ne 0 ]
}

@test "missing instance secrets file: error names release and path" {
  run _render_negative missing-secrets-file
  echo "$output" | grep -q "instance secrets file not found"
  echo "$output" | grep -q "no-such-secrets.sops.yaml"
}
