#!/usr/bin/env bats
#
# Scenario: instance-level inline values (deployment.yaml apps[].values list)
# merge into the release with precedence BETWEEN template-defaults (below) and
# hierarchy values (above):
#
#   chart defaults < template-include < template-defaults < instance inline < hierarchy
#
# The fixture at tests/deployments/cluster1/apps/deployment12/deployment.yaml
# sets four keys that collide at different layers in the chain to probe each
# boundary in the precedence order.

load 'helpers/render'

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

@test "d12: hierarchy (global) wins over instance inline" {
  # global.values.yaml sets globalOnly=fromGlobal; instance tried to set fromInstance.
  # Hierarchy is the last entry in the release values list, so it takes priority.
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .globalOnly
  [ "$output" = "fromGlobal" ]
}
