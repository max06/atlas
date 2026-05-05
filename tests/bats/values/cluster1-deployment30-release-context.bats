#!/usr/bin/env bats
#
# Scenario: release-context access in values templates.
#
# deployment30 exercises the values-loader pipeline where .Release.* is
# available at release-evaluation time alongside the full atlas object.
#
# Assertions cover:
#   - inline values from the app template flow through
#   - .Release.Name and .Release.Namespace resolve inside a values gotmpl
#   - .Values.atlas.instance.* and .Values.atlas.deployment.* are populated

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment30
RELEASE=stage3-probe-release

setup_file() { ensure_rendered; }

@test "d30: release rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d30: inline value from app template flows through" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .inlineFromTemplate
  [ "$output" = "present" ]
}

@test "d30: .Release.Name resolves inside the values gotmpl" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.releaseName
  [ "$output" = "stage3-probe-release" ]
}

@test "d30: .Release.Namespace resolves inside the values gotmpl" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.releaseNamespace
  [ "$output" = "test" ]
}

@test "d30: atlas.instance.template populated in env-values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.instanceTemplate
  [ "$output" = "app-stage3-probe" ]
}

@test "d30: atlas.instance.name populated in env-values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.instanceName
  [ "$output" = "app-stage3-probe" ]
}

@test "d30: atlas.deployment.cluster propagated" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.cluster
  [ "$output" = "cluster1" ]
}

@test "d30: atlas.deployment.deploymentName propagated" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.deploymentName
  [ "$output" = "deployment30" ]
}

