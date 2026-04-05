#!/usr/bin/env bats
#
# Scenario: group-level deployment (deployment6) on group1/cluster2.
# deployment6 lives under deployments/group1/apps/ and is assigned to every
# cluster within group1.
#
# Ported from tests/charts/chart1/tests/group1-cluster2-deployment6-app-group-default_test.yaml

load 'helpers/render'

CLUSTER=group1/cluster2
DEPLOYMENT=deployment6
INSTANCE=app-group

setup_file() { ensure_rendered; }

@test "d6/c2: atlas context assigned to group1/cluster2" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster2" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment6" ]
}

@test "d6/c2: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d6/c2: loads group values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d6/c2: loads cluster2 values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d6/c2: cluster wins overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

@test "d6/c2: decrypts cluster2 sops" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "secretCluster2Value" ]
}
