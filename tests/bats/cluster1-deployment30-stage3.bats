#!/usr/bin/env bats
#
# Scenario: stage-3 engine prototype.
#
# deployment30 opts into the new value-loading pipeline via `engine: stage3`
# at the top of deployment.yaml. Stage-2 fans the deployment out to one
# sub-helmfile per app instance (helmfile.instance.yaml.gotmpl) and the
# shared values-loader (helmfile.values-loader.yaml.gotmpl) resolves the
# actual values at helmfile's release-evaluation time, where .Release.* is
# available alongside the full atlas object.
#
# Assertions cover:
#   - inline values from the app template flow through
#   - .Release.Name and .Release.Namespace resolve inside a values gotmpl
#   - .Values.atlas.instance.* and .Values.atlas.deployment.* are populated
#   - hierarchy merge (global -> cluster -> deployment) still works

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment30
RELEASE=stage3-probe-release

setup_file() { ensure_rendered; }

@test "d30: stage-3 release rendered" {
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

@test "d30: atlas.deployment.cluster propagated to stage-3" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.cluster
  [ "$output" = "cluster1" ]
}

@test "d30: atlas.deployment.deploymentName propagated to stage-3" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .releaseAware.deploymentName
  [ "$output" = "deployment30" ]
}

@test "d30: hierarchy global value reaches the release" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .globalOnly
  [ "$output" = "fromGlobal" ]
}

@test "d30: hierarchy cluster value reaches the release" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .clusterOnly
  [ "$output" = "fromCluster" ]
}

@test "d30: hierarchy override chain (global<-cluster) still wins at the cluster level" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .overrideAll
  [ "$output" = "cluster" ]
}
