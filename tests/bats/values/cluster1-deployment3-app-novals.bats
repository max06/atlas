#!/usr/bin/env bats
#
# Scenario: standalone cluster (cluster1), deployment WITHOUT deployment-level values.
# Template: app-novals (no template-level values).
# Chain: chart defaults → global → cluster (deployment level is empty).
# Verifies that missing value files are silently skipped and that cluster
# becomes the highest-priority source in their absence.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment3-app-novals-default_test.yaml

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment3
INSTANCE=app-novals

setup_file() { ensure_rendered; }

@test "d3: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d3: does NOT load group values for standalone cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "null" ]
}

@test "d3: loads cluster values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d3: no deployment-only values (no deployment file)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deploymentOnly
  [ "$output" = "null" ]
}

@test "d3: cluster wins overrideAll when no deployment values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "cluster" ]
}

@test "d3: cluster wins overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

@test "d3: global wins overrideGroupUp (no group)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideGroupUp
  [ "$output" = "global" ]
}

@test "d3: cluster wins nested.shared (no deployment override)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.shared
  [ "$output" = "cluster" ]
}

@test "d3: no nested.fromDeployment" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromDeployment
  [ "$output" = "null" ]
}

@test "d3: nested merges global + cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGlobal
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromCluster
  [ "$output" = "true" ]
}

@test "d3: decrypts sops from global and cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGlobal
  [ "$output" = "secretGlobalValue" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "secretCluster1Value" ]
}

@test "d3: no deployment-level sops values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsDeployment
  [ "$output" = "null" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsMap
  [ "$output" = "null" ]
}

@test "d3: cluster sops overrides global sops for sopsOverride" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsOverride
  [ "$output" = "cluster" ]
}

@test "d3: sops nested deep merge preserves cluster deep key" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.secretKey
  [ "$output" = "cluster1NestedSecret" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.deep.verySecret
  [ "$output" = "hiddenValue" ]
}

@test "d3: atlas deployment context" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment3" ]
}
