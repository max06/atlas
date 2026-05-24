#!/usr/bin/env bats
#
# Scenario: instance-level list field overrides (jsonPatches from apps[]).
#
# The app-patches template defines strategicMergePatches at the
# release level (adding annotation atlas-test/patched=yes). deployment45
# adds an instance-level jsonPatches entry that should be appended to the
# release — proving the applyListOverride append path works for
# instance-sourced overrides.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment45
RELEASE=stage3-patches-release

setup_file() { ensure_rendered; }

@test "d45: release rendered" {
  instance_rendered_any "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d45: template-level strategicMergePatches still applied" {
  run render_contains "$CLUSTER" "$DEPLOYMENT" "$RELEASE" "atlas-test/patched"
  [ "$status" -eq 0 ]
}

@test "d45: instance-level jsonPatches annotation applied" {
  run render_contains "$CLUSTER" "$DEPLOYMENT" "$RELEASE" "atlas-test/from-instance"
  [ "$status" -eq 0 ]
}
