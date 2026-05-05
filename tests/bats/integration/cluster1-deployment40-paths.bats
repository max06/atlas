#!/usr/bin/env bats
#
# Scenario: resolves template-relative paths for scalar/nested-key
# release fields:
#   - keyring          (single scalar)
#   - set[].file       (nested key inside a list of maps)
#   - setString[].file (same shape as set[])
#
# helm receives these via the helmfile state and reads them at install/
# upgrade time. Without resolution they would be looked up in the
# consumer's CWD, not the template dir, and either fail or pick up the
# wrong file. The build state must carry absolute paths.

CLUSTER=cluster1
DEPLOYMENT=deployment40
RELEASE=stage3-paths-release

# Cache the build-state YAML for our release once per file.
setup_file() {
  local root
  root="$(cd "${BATS_TEST_DIRNAME}/../../.." && pwd)"
  D40_RELEASE_YAML="$(
    helmfile -f "$root/helmfile.yaml.gotmpl" \
      --state-values-set "atlas.appTemplates=tests/templates" \
      --state-values-set "atlas.deploymentDefinitions=tests/deployments" \
      --state-values-set "atlas.cwd=$root" \
      build --selector "cluster=$CLUSTER,deploymentName=$DEPLOYMENT" 2>/dev/null \
        | yq "select(.releases != null) | .releases[] | select(.name == \"$RELEASE\")"
  )"
  export D40_RELEASE_YAML
}

@test "d40: keyring resolved to absolute path under template dir" {
  run yq '.keyring' <<<"$D40_RELEASE_YAML"
  [[ "$output" == /*tests/templates/app-paths/*keyring.gpg ]]
}

@test "d40: set[].file resolved to absolute path under template dir" {
  run yq '.set[0].file' <<<"$D40_RELEASE_YAML"
  [[ "$output" == /*tests/templates/app-paths/*set-foo.txt ]]
}

@test "d40: setString[].file resolved to absolute path under template dir" {
  run yq '.setString[0].file' <<<"$D40_RELEASE_YAML"
  [[ "$output" == /*tests/templates/app-paths/*setstr-bar.txt ]]
}

@test "d40: set[].name passed through unchanged" {
  run yq '.set[0].name' <<<"$D40_RELEASE_YAML"
  [ "$output" = "foo" ]
}
