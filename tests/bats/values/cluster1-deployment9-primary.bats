#!/usr/bin/env bats
#
# Scenario: duplicate-template deployment (deployment9), first instance (primary).
# deployment9 uses app-named twice, disambiguated by instance name. Validates
# that atlas.instance.name is set correctly and release naming uses the name
# suffix.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment9-primary-default_test.yaml

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment9
INSTANCE=primary-app-named

setup_file() { ensure_rendered; }

@test "d9/primary: release was rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$INSTANCE"
}

@test "d9/primary: atlas.instance.name = primary" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.instance.name
  [ "$output" = "primary" ]
}

@test "d9/primary: atlas.instance.template = app-named" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.instance.template
  [ "$output" = "app-named" ]
}

