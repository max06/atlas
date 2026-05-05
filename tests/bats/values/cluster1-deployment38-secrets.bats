#!/usr/bin/env bats
#
# Scenario: release.secrets and merges them last.
#
# Helmfile's env-level secrets contract: "non-HCL secrets are loaded first
# but merged last". ATLAS replicates this at the release level — every
# entry in release.secrets and apps[].secrets lands AFTER all values:
# entries are merged.
#
# Fixture (deployment38 / app-secrets):
#   key1 — only in template values
#   key2 — template values + template secrets (secret wins)
#   key3 — template secrets + instance secrets (instance wins)

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment38
RELEASE=stage3-secrets-release

setup_file() { ensure_rendered; }

@test "d38: release rendered" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d38: key1 from values survives (no secret override)" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .key1
  [ "$output" = "fromValues" ]
}

@test "d38: key2 — template secret beats template values" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .key2
  [ "$output" = "fromTemplateSecret" ]
}

@test "d38: key3 — instance secret beats template secret" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .key3
  [ "$output" = "fromInstanceSecret" ]
}

@test "d38: helmfile build state strips release.secrets (loader handled them)" {
  root="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  run bash -c "helmfile -f '$root/helmfile.yaml.gotmpl' \
    --state-values-set 'atlas.appTemplates=tests/templates' \
    --state-values-set 'atlas.deploymentDefinitions=tests/deployments' \
    --state-values-set \"atlas.cwd=$root\" \
    build --selector cluster=$CLUSTER,deploymentName=$DEPLOYMENT 2>/dev/null \
    | yq 'select(.releases != null) | .releases[] | select(.name == \"$RELEASE\") | has(\"secrets\")'"
  [ "$output" = "false" ]
}
