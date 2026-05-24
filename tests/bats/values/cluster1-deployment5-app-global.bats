#!/usr/bin/env bats
#
# Scenario: global deployment (deployment5) rendered on standalone cluster1.
# Template: app-global (no template-level values).
# Verifies a global-level deployment lands on each cluster with that cluster's
# own atlas context and values.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment5-app-global-default_test.yaml

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment5
INSTANCE=app-global

setup_file() { ensure_rendered; }

@test "d5/c1: atlas context assigned to cluster1" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment5" ]
}

@test "d5/c1: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d5/c1: no group values (standalone cluster)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "null" ]
}

@test "d5/c1: loads cluster1 values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d5/c1: cluster wins overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

@test "d5/c1: decrypts global sops" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGlobal
  [ "$output" = "secretGlobalValue" ]
}

@test "d5/c1: decrypts cluster1 sops" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "secretCluster1Value" ]
}
