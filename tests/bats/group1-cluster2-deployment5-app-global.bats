#!/usr/bin/env bats
#
# Scenario: global deployment (deployment5) on grouped cluster group1/cluster2.
# The same global deployment appears on every cluster, each with its own
# group+cluster context and values.
#
# Ported from tests/charts/chart1/tests/group1-cluster2-deployment5-app-global-default_test.yaml

load 'helpers/render'

CLUSTER=group1/cluster2
DEPLOYMENT=deployment5
INSTANCE=app-global

setup_file() { ensure_rendered; }

@test "d5/c2: atlas context assigned to group1/cluster2" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster2" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment5" ]
}

@test "d5/c2: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d5/c2: loads group values (grouped cluster)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d5/c2: loads cluster2 values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d5/c2: group wins overrideGroupUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideGroupUp
  [ "$output" = "group" ]
}

@test "d5/c2: cluster wins overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

@test "d5/c2: decrypts group sops (sopsGroup)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGroup
  [ "$output" = "secretGroupValue" ]
}

@test "d5/c2: decrypts cluster2 sops (distinct from cluster1)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "secretCluster2Value" ]
}
