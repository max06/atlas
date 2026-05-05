#!/usr/bin/env bats
#
# Scenario: ATLAS emits `template` and `instance` commonLabels.
#
# In addition to the existing cluster/clusterName/deploymentName
# labels, ATLAS auto-injects two more so consumers can filter by app
# template family and named instance:
#   - template: the app-template directory name
#   - instance: the apps[].name value (or template name when name unset)
#
# Covers two cases:
#   - deployment30 — apps[].name unset; instance label defaults to template.
#   - deployment33 — apps[].name explicit; instance differs from template.
#
# Release labels live in helmfile build state (not rendered manifests), so
# we use release_labels which queries `helmfile build`.

load 'helpers/render'

@test "default-name: template label = app-probe" {
  labels="$(release_labels cluster1 deployment30 stage3-probe-release)"
  run yq '.template' <<<"$labels"
  [ "$output" = "app-probe" ]
}

@test "default-name: instance label defaults to template" {
  labels="$(release_labels cluster1 deployment30 stage3-probe-release)"
  run yq '.instance' <<<"$labels"
  [ "$output" = "app-probe" ]
}

@test "named-primary: template label = app-named-multi" {
  labels="$(release_labels cluster1 deployment33 primary-stage3-named)"
  run yq '.template' <<<"$labels"
  [ "$output" = "app-named-multi" ]
}

@test "named-primary: instance label = primary" {
  labels="$(release_labels cluster1 deployment33 primary-stage3-named)"
  run yq '.instance' <<<"$labels"
  [ "$output" = "primary" ]
}

@test "named-secondary: instance label = secondary" {
  labels="$(release_labels cluster1 deployment33 secondary-stage3-named)"
  run yq '.instance' <<<"$labels"
  [ "$output" = "secondary" ]
}
