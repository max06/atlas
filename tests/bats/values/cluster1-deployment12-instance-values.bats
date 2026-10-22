#!/usr/bin/env bats
#
# Scenario: instance-level inline values (deployment.yaml apps[].values list)
# are the most specific layer. They win over the template AND over every file
# of the directory hierarchy, the deployment's own values.yaml included:
#
#   chart defaults < template-include < template-defaults < hierarchy < instance inline
#
# Decision (max06/atlas#86, v0.10.0): before, the hierarchy overrode the
# instance. The assertion "hierarchy (global) wins over instance inline" of
# this file was flipped on purpose.
#
# The fixture at tests/deployments/cluster1/apps/deployment12/deployment.yaml
# sets keys that collide at different layers in the chain to probe each
# boundary in the precedence order.

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment12
INSTANCE=app1

setup_file() { ensure_rendered; }

@test "d12: unique instance key is delivered to the release" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .instanceOnly
  [ "$output" = "fromInstance" ]
}

@test "d12: instance inline wins over template-include" {
  # app1/values.yaml.gotmpl sets templateInclude=fromInclude; instance sets fromInstance.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateInclude
  [ "$output" = "fromInstance" ]
}

@test "d12: instance inline wins over template-defaults" {
  # app1 inline values set templateDefault=fromDefault; instance sets fromInstance.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .templateDefault
  [ "$output" = "fromInstance" ]
}

@test "d12: instance inline wins over hierarchy (global)" {
  # global.values.yaml sets globalOnly=fromGlobal; the instance sets fromInstance.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromInstance" ]
}

@test "d12: instance inline wins over hierarchy (cluster)" {
  # cluster1/cluster.values.yaml sets clusterOnly=fromCluster; the instance sets fromInstance.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .clusterOnly
  [ "$output" = "fromInstance" ]
}

@test "d12: instance inline wins over the deployment's own values.yaml" {
  # deployment12/values.yaml sets deploymentShared=fromDeployment; the instance sets fromInstance.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deploymentShared
  [ "$output" = "fromInstance" ]
}

@test "d12: the deployment's values.yaml is still delivered" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .deploymentOnly
  [ "$output" = "fromDeployment" ]
}
