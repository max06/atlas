#!/usr/bin/env bats
#
# Scenario: multi-template deployment (deployment7), first app (app1).
# Validates that when deployment.yaml lists multiple apps, each is rendered
# independently with its own template values + shared hierarchy values.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment7-app1-default_test.yaml

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment7
INSTANCE=app1

setup_file() { ensure_rendered; }

@test "d7/app1: template-include from app1" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateInclude
  [ "$output" = "fromInclude" ]
}

@test "d7/app1: template-defaults from app1" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateDefault
  [ "$output" = "fromDefault" ]
}

@test "d7/app1: hierarchy wins overrideAll (cluster, no deployment values)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "cluster" ]
}

@test "d7/app1: loads global and cluster values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d7/app1: atlas context for deployment7" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment7" ]
}
