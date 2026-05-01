#!/usr/bin/env bats
#
# Scenario: top-level fields in the user's app template that ATLAS does
# not own (repositories, helmDefaults, templates, bases, hooks) must
# pass through verbatim to the emitted per-instance helmfile state.
# Otherwise releases that reference a registered repo (`chart: repo/x`)
# fail to resolve, helmDefaults silently revert to helmfile's built-in
# defaults, and any other user-supplied state-level config is lost.

CLUSTER=cluster1
DEPLOYMENT=deployment44
RELEASE=stage3-passthrough

# Single helmfile build call shared across the assertions in this file.
# The output is large (helmfile build emits the full resolved state for
# every sub-helmfile concatenated as multi-doc YAML), so we stash it in
# a file instead of an exported variable — bats forks bats-exec-test per
# @test and exec'd env vars count against the kernel's ARG_MAX, which
# the build output blows past around a few thousand lines, surfacing as
# the cryptic "/usr/local/libexec/bats-core/bats-exec-test: Argument
# list too long".
D44_BUILD_FILE="${BATS_FILE_TMPDIR:-/tmp}/d44-build.yaml"
export D44_BUILD_FILE

setup_file() {
  local root
  root="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  helmfile -f "$root/helmfile.yaml.gotmpl" \
    --state-values-set "atlas.appTemplates=tests/templates" \
    --state-values-set "atlas.deploymentDefinitions=tests/deployments" \
    --state-values-set "atlas.cwd=$root" \
    build --selector "cluster=$CLUSTER,deploymentName=$DEPLOYMENT" \
    > "$D44_BUILD_FILE" 2>/dev/null
}

@test "d44: helmDefaults.timeout from user template appears in emitted state" {
  run yq 'select(.helmDefaults != null) | .helmDefaults.timeout' "$D44_BUILD_FILE"
  [ "$output" = "123" ]
}

@test "d44: helmDefaults.wait from user template appears in emitted state" {
  run yq 'select(.helmDefaults != null) | .helmDefaults.wait' "$D44_BUILD_FILE"
  [ "$output" = "false" ]
}
