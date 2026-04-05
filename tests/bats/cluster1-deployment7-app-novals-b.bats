#!/usr/bin/env bats
#
# Scenario: multi-template deployment (deployment7), second app (app-novals-b).
# Validates app-novals-b gets only hierarchy values (no template values) and
# does NOT leak app1's template values. Proves instance isolation across apps
# in a single deployment.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment7-app-novals-b-default_test.yaml

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment7
INSTANCE=app-novals-b

setup_file() { ensure_rendered; }

@test "d7/app-novals-b: no templateInclude (template defines none)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateInclude
  [ "$output" = "null" ]
}

@test "d7/app-novals-b: no templateDefault" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateDefault
  [ "$output" = "null" ]
}

@test "d7/app-novals-b: cluster is highest override for overrideAll" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "cluster" ]
}

@test "d7/app-novals-b: loads global and cluster values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d7/app-novals-b: atlas context for deployment7" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment7" ]
}
