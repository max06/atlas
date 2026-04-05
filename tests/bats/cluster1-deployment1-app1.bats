#!/usr/bin/env bats
#
# Scenario: standalone cluster (cluster1) with full inheritance chain.
# Template: app1 (has both template-include and template-defaults values).
# Precedence (lowest → highest):
#   chart defaults → template-include → template-defaults
#   → global (sops, yaml, gotmpl) → cluster (sops, yaml, gotmpl)
#   → deployment (sops, yaml)
# Group level is SKIPPED: cluster1 is standalone (no "/" in its path).
#
# Ported from tests/charts/chart1/tests/cluster1-deployment1-app1-default_test.yaml

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment1
INSTANCE=app1

setup_file() {
  ensure_rendered
}

# --- Template-level values -------------------------------------------------

@test "app1/d1: loads template-include values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateInclude
  [ "$output" = "fromInclude" ]
}

@test "app1/d1: loads template-defaults values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateDefault
  [ "$output" = "fromDefault" ]
}

@test "app1/d1: template-defaults overrides template-include for shared key" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateOverride
  [ "$output" = "default" ]
}

# --- Hierarchy-level values ------------------------------------------------

@test "app1/d1: loads global values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "app1/d1: does NOT load group values for standalone cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .groupOnly
  [ "$output" = "null" ]
}

@test "app1/d1: loads cluster values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "app1/d1: loads deployment values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deploymentOnly
  [ "$output" = "fromDeployment" ]
}

# --- Override precedence ---------------------------------------------------

@test "app1/d1: keeps global value for overrideGroupUp when no group exists" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideGroupUp
  [ "$output" = "global" ]
}

@test "app1/d1: cluster overrides global for overrideClusterUp" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideClusterUp
  [ "$output" = "cluster" ]
}

@test "app1/d1: deployment overrides all levels (incl. template) for overrideAll" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .overrideAll
  [ "$output" = "deployment" ]
}

@test "app1/d1: global gotmpl overrides global yaml" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .gotmplTest
  [ "$output" = "gotmpl" ]
}

@test "app1/d1: global gotmpl references earlier sops value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .gotmplFromSops
  [ "$output" = "secretGlobalValue" ]
}

@test "app1/d1: global gotmpl references earlier yaml value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .gotmplFromYaml
  [ "$output" = "fromGlobal" ]
}

# --- Cluster-level gotmpl (standalone cluster, no group) ------------------

@test "app1/d1: cluster gotmpl resolves global yaml value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "app1/d1: cluster gotmpl resolves global sops value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterGotmplFromGlobalSops
  [ "$output" = "secretGlobalValue" ]
}

# --- Deployment-level gotmpl ----------------------------------------------

@test "app1/d1: deploy gotmpl resolves global yaml value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromGlobalYaml
  [ "$output" = "fromGlobal" ]
}

@test "app1/d1: deploy gotmpl resolves cluster yaml value" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromClusterYaml
  [ "$output" = "fromCluster" ]
}

@test "app1/d1: deploy gotmpl chains through cluster gotmpl" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deployGotmplFromClusterGotmpl
  [ "$output" = "fromGlobal" ]
}

# --- Deep merge ------------------------------------------------------------

@test "app1/d1: deep merges nested.fromGlobal" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGlobal
  [ "$output" = "true" ]
}

@test "app1/d1: no nested.fromGroup for standalone cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromGroup
  [ "$output" = "null" ]
}

@test "app1/d1: deep merges nested.fromCluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromCluster
  [ "$output" = "true" ]
}

@test "app1/d1: deep merges nested.fromDeployment" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.fromDeployment
  [ "$output" = "true" ]
}

@test "app1/d1: deployment wins nested shared key" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .nested.shared
  [ "$output" = "deployment" ]
}

# --- SOPS decryption & type preservation ----------------------------------

@test "app1/d1: sops string from global (sopsGlobal)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGlobal
  [ "$output" = "secretGlobalValue" ]
}

@test "app1/d1: sops string from global (sopsString)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsString
  [ "$output" = "hello-from-sops" ]
}

@test "app1/d1: sops number preserves int type" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNumber
  [ "$output" = "42" ]
}

@test "app1/d1: sops boolean preserves bool type" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsBool
  [ "$output" = "true" ]
}

@test "app1/d1: sops float from cluster level" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsFloat
  [ "$output" = "3.14" ]
}

@test "app1/d1: sops map from deployment (username)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsMap.username
  [ "$output" = "admin" ]
}

@test "app1/d1: sops map from deployment (password)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsMap.password
  [ "$output" = "s3cret!" ]
}

@test "app1/d1: sops map from deployment (port, int type)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsMap.port
  [ "$output" = "5432" ]
}

@test "app1/d1: sops unique keys per level — sopsCluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsCluster
  [ "$output" = "secretCluster1Value" ]
}

@test "app1/d1: sops unique keys per level — sopsDeployment" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsDeployment
  [ "$output" = "secretDep1Value" ]
}

@test "app1/d1: no sopsGroup for standalone cluster" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsGroup
  [ "$output" = "null" ]
}

@test "app1/d1: deployment sops wins sopsOverride across levels" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsOverride
  [ "$output" = "deployment" ]
}

@test "app1/d1: sops deep merge — secretKey from deployment" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.secretKey
  [ "$output" = "dep1NestedSecret" ]
}

@test "app1/d1: sops deep merge — level = deployment" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.level
  [ "$output" = "deployment" ]
}

@test "app1/d1: sops deep merge — nested.deep.verySecret" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .sopsNested.deep.verySecret
  [ "$output" = "hiddenValue" ]
}

# --- Atlas context --------------------------------------------------------

@test "app1/d1: atlas.deployment.cluster is set" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
}

@test "app1/d1: atlas.deployment.deploymentName is set" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment1" ]
}
