#!/usr/bin/env bats
#
# Scenario: duplicate-template deployment (deployment9), second instance (secondary).
# Proves the second named instance gets its OWN atlas.instance.name — instances
# don't share state.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment9-secondary-default_test.yaml

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment9
INSTANCE=app-named-secondary

setup_file() { ensure_rendered; }

@test "d9/secondary: release was rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$INSTANCE"
}

@test "d9/secondary: atlas.instance.name = secondary" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.instance.name
  [ "$output" = "secondary" ]
}

@test "d9/secondary: atlas.instance.template = app-named" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.instance.template
  [ "$output" = "app-named" ]
}

@test "d9/secondary: loads same hierarchy values as primary" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d9/secondary: atlas deployment context (same as primary)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment9" ]
}
