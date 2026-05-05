#!/usr/bin/env bats
#
# Scenario: ATLAS defaults release.skipSchemaValidation to true.
#
# Schema validation runs only on helm install/upgrade/lint — not on the
# template/build paths ATLAS exercises during ArgoCD render — so it
# primarily matters at consumer-side bootstrap. Defaulting it on removes
# a common source of false-positive failures when CRDs aren't yet
# installed. User intent (explicit true OR explicit false) wins over the
# default.

load 'helpers/render'

CLUSTER=cluster1
DEPLOYMENT=deployment37

setup_file() { ensure_rendered; }

# helmfile build state — we read .skipSchemaValidation per release.

_build_release_field() {
  local release="$1" field="$2" root
  root="$(_repo_root)"
  helmfile -f "$root/tests/helmfile.yaml.gotmpl" \
    build --selector "cluster=$CLUSTER,deploymentName=$DEPLOYMENT" 2>/dev/null |
      yq "select(.releases != null) | .releases[] | select(.name == \"$release\") | .$field"
}

@test "d37: schema-default-release has skipSchemaValidation defaulted to true" {
  run _build_release_field schema-default-release skipSchemaValidation
  [ "$output" = "true" ]
}

@test "d37: schema-explicit-false preserves user-set false" {
  run _build_release_field schema-explicit-false skipSchemaValidation
  [ "$output" = "false" ]
}
