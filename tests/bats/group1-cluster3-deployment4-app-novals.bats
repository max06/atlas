#!/usr/bin/env bats
#
# Scenario: grouped cluster (group1/cluster3) WITHOUT cluster or deployment values.
# Template: app-novals (no template-level values).
# Chain: chart → global → group (cluster3 has no values file, deployment4 has none either).
# Tests that with both cluster+deployment absent, the group level is top.
#
# Ported from tests/charts/chart1/tests/group1-cluster3-deployment4-app-novals-default_test.yaml

load 'helpers/render'

CLUSTER=group1/cluster3
DEPLOYMENT=deployment4
INSTANCE=app-novals

setup_file() { ensure_rendered; }

@test "d4/c3: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d4/c3: loads group values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d4/c3: no cluster values (cluster3 has no values file)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "null" ]
}

@test "d4/c3: no deployment values (deployment4 has no values file)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deploymentOnly
  [ "$output" = "null" ]
}

@test "d4/c3: group wins overrideAll" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "group" ]
}

@test "d4/c3: group wins overrideGroupUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideGroupUp
  [ "$output" = "group" ]
}

@test "d4/c3: group wins overrideClusterUp (no cluster)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "group" ]
}

@test "d4/c3: group wins nested.shared" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.shared
  [ "$output" = "group" ]
}

@test "d4/c3: nested merges only global + group" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGlobal
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGroup
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromCluster
  [ "$output" = "null" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromDeployment
  [ "$output" = "null" ]
}

@test "d4/c3: decrypts global and group sops" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGlobal
  [ "$output" = "secretGlobalValue" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGroup
  [ "$output" = "secretGroupValue" ]
}

@test "d4/c3: sops list from group level" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.sopsList[0]'
  [ "$output" = "itemOne" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.sopsList[1]'
  [ "$output" = "itemTwo" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.sopsList[2]'
  [ "$output" = "itemThree" ]
}

@test "d4/c3: no cluster or deployment sops values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "null" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsDeployment
  [ "$output" = "null" ]
}

@test "d4/c3: group sops wins sopsOverride" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsOverride
  [ "$output" = "group" ]
}

@test "d4/c3: sops nested deep merges global + group" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.secretKey
  [ "$output" = "groupNestedSecret" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.level
  [ "$output" = "group" ]
}

@test "d4/c3: atlas deployment context" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster3" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment4" ]
}
