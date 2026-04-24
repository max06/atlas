#!/usr/bin/env bats
#
# Scenario: cluster-identification values/labels exposed to app templates.
#
#   atlas.deployment.cluster       — full path (e.g. "group1/cluster2")
#   atlas.deployment.clusterName   — leaf only (e.g. "cluster2"); slash-free
#                                    so it's safe for ArgoCD app names, etc.
#   atlas.deployment.clusterGroup  — group prefix (e.g. "group1"), or ABSENT
#                                    when the cluster is standalone. Templates
#                                    use its presence to gate group-scoped
#                                    features.
#
# All three are derived mechanically from the cluster path in
# helmfile.all.yaml.gotmpl, so two cases (standalone + grouped) cover the
# behaviour for all deployments without needing a dedicated fixture.

load 'helpers/render'

setup_file() {
  ensure_rendered
  # release_labels runs `helmfile build`, which is expensive. Cache once per
  # file for the commonLabels assertions below.
  export CLUSTERNAME_LABELS_GROUPED="$(release_labels group1/cluster2 deployment2 app1)"
  export CLUSTERNAME_LABELS_STANDALONE="$(release_labels cluster1 deployment1 app1)"
}

@test "clusterName: standalone cluster — value equals the cluster path" {
  run get_path cluster1 deployment1 app1 .atlas.deployment.clusterName
  [ "$output" = "cluster1" ]
}

@test "clusterName: grouped cluster — leaf name only (group prefix stripped)" {
  run get_path group1/cluster2 deployment2 app1 .atlas.deployment.clusterName
  [ "$output" = "cluster2" ]
}

@test "clusterName: grouped cluster — differs from full cluster path" {
  # Sanity: the two keys must diverge for a grouped cluster, otherwise
  # clusterName would be redundant.
  run get_path group1/cluster2 deployment2 app1 .atlas.deployment.cluster
  [ "$output" = "group1/cluster2" ]
}

@test "clusterName: release commonLabels include clusterName (grouped)" {
  run yq '.clusterName' <<<"$CLUSTERNAME_LABELS_GROUPED"
  [ "$output" = "cluster2" ]
}

@test "clusterName: release commonLabels include clusterName (standalone)" {
  run yq '.clusterName' <<<"$CLUSTERNAME_LABELS_STANDALONE"
  [ "$output" = "cluster1" ]
}

@test "clusterGroup: grouped cluster exposes group prefix in values" {
  run get_path group1/cluster2 deployment2 app1 .atlas.deployment.clusterGroup
  [ "$output" = "group1" ]
}

@test "clusterGroup: standalone cluster omits the key entirely" {
  # yq emits "null" when a key is absent — that's the contract we rely on
  # in consumer templates via `{{ if .Values.atlas.deployment.clusterGroup }}`.
  run get_path cluster1 deployment1 app1 .atlas.deployment.clusterGroup
  [ "$output" = "null" ]
}

@test "clusterGroup: grouped cluster carries label in commonLabels" {
  run yq '.clusterGroup' <<<"$CLUSTERNAME_LABELS_GROUPED"
  [ "$output" = "group1" ]
}

@test "clusterGroup: standalone cluster has no clusterGroup label" {
  run yq '.clusterGroup' <<<"$CLUSTERNAME_LABELS_STANDALONE"
  [ "$output" = "null" ]
}
