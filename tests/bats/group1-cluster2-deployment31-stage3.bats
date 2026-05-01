#!/usr/bin/env bats
#
# Scenario: stage-3 engine on a grouped cluster (group1/cluster2). Asserts
# the loader walks the hierarchy at all four levels including the group
# level (only present when cluster path contains "/"), and that .yaml.gotmpl
# variants at every level are tpl-rendered with progressive context — i.e.
# group.values.yaml.gotmpl can reference keys defined by global.values.yaml,
# and cluster.values.yaml.gotmpl can reference keys from the group level.
#
# Mirrors the non-SOPS half of group1-cluster2-deployment2-app1.bats; SOPS
# coverage is added in a later stage-3 milestone.

load 'helpers/render'

CLUSTER=group1/cluster2
DEPLOYMENT=deployment31
RELEASE=stage3-probe-release

setup_file() { ensure_rendered; }

# --- Hierarchy plain .yaml at every level ---------------------------------

@test "d31: loads global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d31: loads group yaml (grouped cluster)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d31: loads cluster yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

# --- .yaml.gotmpl variants at every level ---------------------------------

@test "d31: global .yaml.gotmpl resolves earlier global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .gotmplFromYaml
  [ "$output" = "fromGlobal" ]
}

@test "d31: group .yaml.gotmpl resolves global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .groupGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "d31: cluster .yaml.gotmpl resolves global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .clusterGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "d31: cluster .yaml.gotmpl resolves group yaml (chained context)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .clusterGotmplFromGroupYaml
  [ "$output" = "fromGroup" ]
}

@test "d31: cluster .yaml.gotmpl chains through group .yaml.gotmpl" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .clusterGotmplFromGroupGotmpl
  [ "$output" = "fromGlobal" ]
}

# --- Override precedence across levels ------------------------------------

@test "d31: group overrides global for overrideGroupUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .overrideGroupUp
  [ "$output" = "group" ]
}

@test "d31: cluster overrides group for overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

# --- Nested deep merge across global + group + cluster --------------------

@test "d31: nested merges global + group + cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .nested.fromGlobal
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .nested.fromGroup
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .nested.fromCluster
  [ "$output" = "true" ]
}

@test "d31: cluster wins nested.shared" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .nested.shared
  [ "$output" = "cluster" ]
}

# --- Atlas context unchanged for grouped cluster --------------------------

@test "d31: atlas.deployment.cluster carries the full path" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.cluster
  [ "$output" = "group1/cluster2" ]
}

@test "d31: atlas.deployment.deploymentName matches" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.deploymentName
  [ "$output" = "deployment31" ]
}
