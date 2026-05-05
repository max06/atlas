#!/usr/bin/env bats
#
# Scenario: ATLAS strips `condition` and `installed` from releases.
#
# In ATLAS, deployment intent is expressed by the presence of a deployment.yaml
# entry, not by helmfile-level conditionals. Letting `condition:` or
# `installed:` survive into the rendered helmfile state would cause ArgoCD to
# observe an empty deployment for app instances that explicitly opted in,
# which is surprising and indistinguishable from a misconfiguration.
#
# The app template here sets both fields so vanilla helmfile would skip the
# release. ATLAS must drop those fields and render normally; if either field
# leaks through, helmfile produces no manifest and the first assertion fails.

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment35
RELEASE=condinst-release

setup_file() { ensure_rendered; }

@test "d35: condition+installed stripped — release renders despite both being set" {
  instance_rendered "$CLUSTER" "$DEPLOYMENT" "$RELEASE"
}

@test "d35: inline value flows through after strip" {
  run get_path "$CLUSTER" "$DEPLOYMENT" "$RELEASE" .rendered
  [ "$output" = "yes" ]
}

@test "d35: helmfile build state has no condition: field" {
  root="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  run bash -c "helmfile -f '$root/tests/helmfile.yaml.gotmpl' \
    build --selector cluster=$CLUSTER,deploymentName=$DEPLOYMENT 2>/dev/null \
    | yq 'select(.releases != null) | .releases[] | select(.name == \"$RELEASE\") | has(\"condition\")'"
  [ "$output" = "false" ]
}

@test "d35: helmfile build state has no installed: field" {
  root="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  run bash -c "helmfile -f '$root/tests/helmfile.yaml.gotmpl' \
    build --selector cluster=$CLUSTER,deploymentName=$DEPLOYMENT 2>/dev/null \
    | yq 'select(.releases != null) | .releases[] | select(.name == \"$RELEASE\") | has(\"installed\")'"
  [ "$output" = "false" ]
}
