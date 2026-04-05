#!/usr/bin/env bats
#
# Scenario: global deployment (deployment5) on grouped cluster group1/cluster3
# which has NO cluster-level values. Group is the highest-priority override.
#
# Ported from tests/charts/chart1/tests/group1-cluster3-deployment5-app-global-default_test.yaml

load 'helpers/render'

CLUSTER=group1/cluster3
DEPLOYMENT=deployment5
INSTANCE=app-global

setup_file() { ensure_rendered; }

@test "d5/c3: atlas context assigned to group1/cluster3" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster3" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment5" ]
}

@test "d5/c3: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d5/c3: loads group values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d5/c3: no cluster-only values (cluster3 has none)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "null" ]
}

@test "d5/c3: group wins overrideClusterUp (no cluster)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "group" ]
}

@test "d5/c3: no cluster sops values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "null" ]
}
