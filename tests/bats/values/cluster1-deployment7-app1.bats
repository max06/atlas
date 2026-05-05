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

