#!/usr/bin/env bats
#
# Scenario: instance-level strategicMergePatches FILE entry (issue #63).
#
# deployment48 references ./instance-patch.yaml next to its deployment.yaml.
# applyListOverride must anchor the path deployment-relative (unanchored it
# resolves against helmfile's cache dir and silently never applies) and the
# patch annotation must land in the rendered ConfigMap.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment48
RELEASE=stage3-patches-release

setup_file() { ensure_rendered; }

@test "d48: release rendered" {
  instance_rendered_any "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d48: template-level strategicMergePatches still applied" {
  run render_contains "$CLUSTER" "$DEPLOYMENT" "$RELEASE" "atlas-test/patched"
  [ "$status" -eq 0 ]
}

@test "d48: instance-level file patch applied (deployment-relative anchor)" {
  run render_contains "$CLUSTER" "$DEPLOYMENT" "$RELEASE" "atlas-test/from-instance-file"
  [ "$status" -eq 0 ]
}
