#!/usr/bin/env bats
#
# Scenario: group-level deployment (deployment6) on group1/cluster3 (no cluster values).
#
# Ported from tests/charts/chart1/tests/group1-cluster3-deployment6-app-group-default_test.yaml

load 'helpers/render'

CLUSTER=group1/cluster3
DEPLOYMENT=deployment6
INSTANCE=app-group

setup_file() { ensure_rendered; }

@test "d6/c3: atlas context assigned to group1/cluster3" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster3" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment6" ]
}

@test "d6/c3: loads group values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d6/c3: no cluster-only values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "null" ]
}

@test "d6/c3: group wins overrideAll" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "group" ]
}

@test "d6/c3: no cluster sops values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "null" ]
}
