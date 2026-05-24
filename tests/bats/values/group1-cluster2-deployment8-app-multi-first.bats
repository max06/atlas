#!/usr/bin/env bats
#
# Scenario: multi-release template (deployment8), first release (app-multi-first).
# The app-multi template defines TWO releases in one helmfile.yaml.gotmpl.
# Each release is independently processed with its own template-defaults +
# shared hierarchy values.
#
# Ported from tests/charts/chart1/tests/group1-cluster2-deployment8-app-multi-default-first_test.yaml

load '../helpers/render'

CLUSTER=group1/cluster2
DEPLOYMENT=deployment8
INSTANCE=app-multi-first

setup_file() { ensure_rendered; }

@test "d8/first: multiRelease = first" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .multiRelease
  [ "$output" = "first" ]
}

@test "d8/first: loads hierarchy values from group1/cluster2" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d8/first: atlas deployment context" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster2" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment8" ]
}
