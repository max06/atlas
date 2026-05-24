#!/usr/bin/env bats
#
# Scenario: grouped cluster (group1/cluster2) with FULL four-level inheritance.
# Template: app1 (template-include + template-defaults).
# Chain: chart → template-include → template-defaults → global → group → cluster → deployment.
# Group level IS loaded because cluster2 lives under group1 (path contains "/").
#
# Ported from tests/charts/chart1/tests/group1-cluster2-deployment2-app1-default_test.yaml

load '../helpers/render'

CLUSTER=group1/cluster2
DEPLOYMENT=deployment2
INSTANCE=app1

setup_file() { ensure_rendered; }

# --- Template-level values -------------------------------------------------

@test "d2: template-include values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateInclude
  [ "$output" = "fromInclude" ]
}

@test "d2: template-defaults values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateDefault
  [ "$output" = "fromDefault" ]
}

@test "d2: template-defaults overrides template-include" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateOverride
  [ "$output" = "default" ]
}

# --- Hierarchy-level values ------------------------------------------------

@test "d2: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d2: loads group values (grouped cluster)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "fromGroup" ]
}

@test "d2: loads cluster values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d2: loads deployment values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deploymentOnly
  [ "$output" = "fromDeployment" ]
}

# --- Override precedence ---------------------------------------------------

@test "d2: group overrides global for overrideGroupUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideGroupUp
  [ "$output" = "group" ]
}

@test "d2: cluster overrides group for overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

@test "d2: deployment overrides all levels for overrideAll" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "deployment" ]
}

@test "d2: global gotmpl overrides global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .gotmplTest
  [ "$output" = "gotmpl" ]
}

@test "d2: global gotmpl references earlier sops value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .gotmplFromSops
  [ "$output" = "secretGlobalValue" ]
}

@test "d2: global gotmpl references earlier yaml value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .gotmplFromYaml
  [ "$output" = "fromGlobal" ]
}

# --- Group-level gotmpl ----------------------------------------------------

@test "d2: group gotmpl resolves global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "d2: group gotmpl resolves global sops" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupGotmplFromGlobalSops
  [ "$output" = "secretGlobalValue" ]
}

# --- Cluster-level gotmpl (grouped) ---------------------------------------

@test "d2: cluster gotmpl resolves global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "d2: cluster gotmpl resolves group yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterGotmplFromGroupYaml
  [ "$output" = "fromGroup" ]
}

@test "d2: cluster gotmpl chains through group gotmpl" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterGotmplFromGroupGotmpl
  [ "$output" = "fromGlobal" ]
}

# --- Deployment-level gotmpl ----------------------------------------------

@test "d2: deploy gotmpl resolves global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "d2: deploy gotmpl resolves group yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromGroupYaml
  [ "$output" = "fromGroup" ]
}

@test "d2: deploy gotmpl resolves cluster yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromClusterYaml
  [ "$output" = "fromCluster" ]
}

@test "d2: deploy gotmpl chains through cluster gotmpl" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromClusterGotmpl
  [ "$output" = "fromGroup" ]
}

# --- Deep merge ------------------------------------------------------------

@test "d2: nested merges all four levels" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGlobal
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGroup
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromCluster
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromDeployment
  [ "$output" = "true" ]
}

@test "d2: deployment wins nested.shared" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.shared
  [ "$output" = "deployment" ]
}

# --- SOPS decryption -------------------------------------------------------

@test "d2: sops string from global (sopsGlobal + sopsString)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGlobal
  [ "$output" = "secretGlobalValue" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsString
  [ "$output" = "hello-from-sops" ]
}

@test "d2: sops number preserves int" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNumber
  [ "$output" = "42" ]
}

@test "d2: sops boolean preserves bool" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsBool
  [ "$output" = "true" ]
}

@test "d2: sops list from group level" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.sopsList[0]'
  [ "$output" = "itemOne" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.sopsList[1]'
  [ "$output" = "itemTwo" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" '.sopsList[2]'
  [ "$output" = "itemThree" ]
}

@test "d2: sops booleans from group level" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsBoolTrue
  [ "$output" = "true" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsBoolFalse
  [ "$output" = "false" ]
}

@test "d2: sops unique keys per level (group/cluster/deployment)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGroup
  [ "$output" = "secretGroupValue" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "secretCluster2Value" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsDeployment
  [ "$output" = "secretDep2Value" ]
}

@test "d2: deployment sops wins sopsOverride" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsOverride
  [ "$output" = "deployment" ]
}

@test "d2: sops nested deep merge across four levels" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.secretKey
  [ "$output" = "dep2NestedSecret" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.level
  [ "$output" = "deployment" ]
}

# --- Atlas context --------------------------------------------------------

@test "d2: atlas deployment context" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "group1/cluster2" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment2" ]
}
