#!/usr/bin/env bats
#
# Scenario: deployment (deployment10) WITHOUT namespace property.
# Template: app-novals (defines namespace: test in its release).
# Verifies that omitting namespace in deployment.yaml does not cause an error
# and the template's own namespace is preserved.
#
# Ported from tests/charts/chart1/tests/cluster1-deployment10-app-novals-default_test.yaml

load '../helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment10
INSTANCE=app-novals

setup_file() { ensure_rendered; }

@test "d10: release was rendered (no namespace in deployment.yaml)" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$INSTANCE"
}

@test "d10: atlas deployment context" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.cluster
  [ "$output" = "cluster1" ]
  run get_path "$CLUSTER" "$DEPLOYMENT" "$INSTANCE" .atlas.deployment.deploymentName
  [ "$output" = "deployment10" ]
}
