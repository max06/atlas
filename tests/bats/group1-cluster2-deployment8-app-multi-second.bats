#!/usr/bin/env bats
#
# Scenario: multi-release template (deployment8), second release (app-multi-second).
# Proves template-defaults are per-release, not shared.
#
# Ported from tests/charts/chart1/tests/group1-cluster2-deployment8-app-multi-default-second_test.yaml

load 'helpers/render'

CLUSTER=group1/cluster2
DEPLOYMENT=deployment8
INSTANCE=app-multi-second

setup_file() { ensure_rendered; }

@test "d8/second: multiRelease = second (not first)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multiRelease
  [ "$output" = "second" ]
}

@test "d8/second: loads same hierarchy values as first" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d8/second: atlas context matches first" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster2" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment8" ]
}
