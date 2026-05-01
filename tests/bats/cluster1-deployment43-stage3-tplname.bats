#!/usr/bin/env bats
#
# Scenario: apps[].name in deployment.yaml uses a hierarchy value.
#
# helmfile.single reads deployment.yaml to dispatch the per-instance
# fan-out — it iterates apps[] and emits one sub-helmfile per app. If
# apps[].name (or .template) is templated against `.Values.<hierarchyKey>`,
# the deployment.yaml render at the helmfile.single level must have the
# merged hierarchy in scope, not just the atlas object.
#
# Without that, the templated name resolves to an empty string, the atlas
# object passed to helmfile.instance carries instance.name="", and the
# downstream pipeline produces a malformed release.
#
# globalOnly = "fromGlobal" (from tests/deployments/global.values.yaml).
# Auto-munge prefixes the release name with the resolved instance name,
# producing "fromGlobal-stage3-tplname".

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment43
RELEASE=tenant-a-stage3-tplname

setup_file() { ensure_rendered; }

@test "d43: deployment.yaml apps[].name resolves a hierarchy value at fan-out time" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d43: rendered release sees its resolved instance.name" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .atlas.instance.name
  [ "$output" = "tenant-a" ]
}
