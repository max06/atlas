#!/usr/bin/env bats
#
# Scenario: an app template that sets release.postRenderer must cause
# ATLAS to fail at render time. ATLAS owns the post-renderer slot for its
# redaction pipeline; helm 4 has no post-renderer chaining; silent
# overwrite would mask intent.
#
# Fixture lives under tests/fixtures-negative/ so the standard bulk render
# in render.bash is not affected. The test invokes helmfile directly with
# its own --state-values-set roots.

_root() { cd "${BATS_TEST_DIRNAME}/../.." && pwd; }

@test "postRenderer in template causes ATLAS to fail render" {
  local root err
  root="$(_root)"
  run bash -c "helmfile -f '$root/helmfile.yaml.gotmpl' \
    --state-values-set 'atlas.appTemplates=tests/templates' \
    --state-values-set 'atlas.deploymentDefinitions=tests/fixtures-negative' \
    --state-values-set \"atlas.cwd=$root\" \
    template --skip-schema-validation 2>&1"
  [ "$status" -ne 0 ]
  echo "$output" | grep -q "postRenderer"
}

@test "fail message names the offending release" {
  local root
  root="$(_root)"
  run bash -c "helmfile -f '$root/helmfile.yaml.gotmpl' \
    --state-values-set 'atlas.appTemplates=tests/templates' \
    --state-values-set 'atlas.deploymentDefinitions=tests/fixtures-negative' \
    --state-values-set \"atlas.cwd=$root\" \
    template --skip-schema-validation 2>&1"
  [ "$status" -ne 0 ]
  echo "$output" | grep -q "pr-release"
}

@test "fail message points to opening an issue" {
  local root
  root="$(_root)"
  run bash -c "helmfile -f '$root/helmfile.yaml.gotmpl' \
    --state-values-set 'atlas.appTemplates=tests/templates' \
    --state-values-set 'atlas.deploymentDefinitions=tests/fixtures-negative' \
    --state-values-set \"atlas.cwd=$root\" \
    template --skip-schema-validation 2>&1"
  [ "$status" -ne 0 ]
  echo "$output" | grep -qi "issue"
}
