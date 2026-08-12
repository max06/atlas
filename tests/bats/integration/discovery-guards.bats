#!/usr/bin/env bats
#
# Scenario: cluster-discovery guard rails (issues #63, #64, #65, #66).
#
# Uses dedicated fixture roots via atlas.deploymentDefinitions overrides —
# same isolation approach as error-paths.bats.

_root() { cd "${BATS_TEST_DIRNAME}/../../.." && pwd; }

_render_fixtures() {
  local fixtures="$1" root; shift
  root="$(_root)"
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.deploymentDefinitions=${fixtures}" \
    template --skip-schema-validation "$@" 2>&1
}

# --- duplicate leaf cluster names (issue #64) ---------------------------------

@test "dup leaf names: render fails" {
  run _render_fixtures fixtures-dup-leaf
  [ "$status" -ne 0 ]
}

@test "dup leaf names: error lists the name and both paths" {
  run _render_fixtures fixtures-dup-leaf
  echo "$output" | grep -q 'duplicate leaf cluster name "dupe"'
  echo "$output" | grep -q "group-a/dupe"
  echo "$output" | grep -q "group-b/dupe"
}

@test "dup leaf names: stage-1 filter does not hide the collision" {
  ATLAS_FILTER_CLUSTER=group-a/dupe run _render_fixtures fixtures-dup-leaf
  [ "$status" -ne 0 ]
  echo "$output" | grep -q 'duplicate leaf cluster name "dupe"'
}

# --- zero-cluster repo: no pseudo-cluster "." (issue #65) ---------------------

@test "global-only repo: no pseudo-cluster render target" {
  run _render_fixtures fixtures-global-only
  # Correct outcome is zero render targets (helmfile: no releases found),
  # NOT a render for pseudo-cluster "." (the pre-fix behavior).
  echo "$output" | grep -q "no releases found"
  ! echo "$output" | grep -q "cluster: \.$"
}

# --- discovery debug dump is opt-in (issue #66) -------------------------------

@test "discovery dump: absent by default" {
  root="$(_root)"
  run env ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME=deployment1 \
    helmfile build -f "$root/tests/helmfile.yaml.gotmpl" --debug
  ! echo "$output" | grep -q "All recognized deployments"
}

@test "discovery dump: present with ATLAS_DEBUG_DISCOVERY" {
  root="$(_root)"
  run env ATLAS_DEBUG_DISCOVERY=1 ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME=deployment1 \
    helmfile build -f "$root/tests/helmfile.yaml.gotmpl" --debug
  echo "$output" | grep -q "All recognized deployments"
}

# --- instance-level patch file missing (issue #63, negative side) -------------

@test "missing instance patch file: render fails with anchored path" {
  root="$(_root)"
  ATLAS_FILTER_CLUSTER=cluster1 ATLAS_FILTER_DEPLOYMENT_NAME=missing-instance-patch \
  run helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.deploymentDefinitions=fixtures-negative" \
    template --skip-schema-validation
  [ "$status" -ne 0 ]
  echo "$output" | grep -q "strategicMergePatches (instance-level): file not found"
  echo "$output" | grep -q "missing-instance-patch/./does-not-exist.yaml"
}
