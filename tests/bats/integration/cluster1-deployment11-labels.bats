#!/usr/bin/env bats
#
# Scenario: instance-level labels from deployment.yaml propagate to release labels.
# Validates that `apps[].labels` in a deployment.yaml is merged into the
# helmfile release state — this is what atlas-template consumers rely on for
# `bootstrap: true` selector matching.
#
# Release labels are NOT injected into rendered Kubernetes manifests by
# `helmfile template`; they live in the helmfile build state and are visible
# via `helmfile build` / `helmfile list`. So we query the build state rather
# than the rendered ConfigMap.

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment11
INSTANCE=app-novals

# Pull labels once per test (not per @test) — helmfile build is expensive.
setup_file() {
  export D11_LABELS="$(release_labels "$CLUSTER" "$DEPLOYMENT" "$INSTANCE")"
}

@test "d11: instance labels include bootstrap=true" {
  run yq '.bootstrap' <<<"$D11_LABELS"
  # K8s label values are always strings; yq strips quotes on output.
  [ "$output" = "true" ]
}

@test "d11: instance labels include role=platform" {
  run yq '.role' <<<"$D11_LABELS"
  [ "$output" = "platform" ]
}

@test "d11: atlas-managed cluster label still present alongside instance labels" {
  # cluster/deploymentName labels are set by helmfile.instance.yaml.gotmpl
  # via commonLabels; they must survive mergeOverwrite with instance labels.
  run yq '.cluster' <<<"$D11_LABELS"
  [ "$output" = "cluster1" ]
}

@test "d11: atlas-managed deploymentName label still present" {
  run yq '.deploymentName' <<<"$D11_LABELS"
  [ "$output" = "deployment11" ]
}
